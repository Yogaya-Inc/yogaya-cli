package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/sevlyar/go-daemon"
)

var (
	location = time.UTC
)

type ExecutionState struct {
	LastExecuted time.Time `json:"last_executed"`
	IsRunning    bool      `json:"is_running"`
	Interval     string    `json:"interval"`
}

const (
	stateFileName = "execution_state.json"
	pidFileName   = "task.pid"
	logsDirName   = "logs"
)

type ExecuteFunc func()

type Daemon struct {
	timeArg     string
	interval    string
	executeFunc ExecuteFunc
}

func NewDaemon(timeArg string, executeFunc ExecuteFunc) (*Daemon, error) {
	if err := ensureLogsDirectory(); err != nil {
		return nil, err
	}

	d := &Daemon{
		timeArg:     timeArg,
		executeFunc: executeFunc,
	}

	if err := d.parseAndValidateTime(); err != nil {
		return nil, err
	}

	return d, nil
}

func (d *Daemon) parseAndValidateTime() error {
	hour, minute, second, err := parseTimeArg(d.timeArg)
	if err != nil {
		return err
	}

	// Create cron expression
	d.interval = fmt.Sprintf("%s %s * * *", minute, hour)
	if second != "00" {
		d.interval = fmt.Sprintf("%s %s %s * * *", second, minute, hour)
	}

	return nil
}

func parseTimeArg(timeArg string) (hour, minute, second string, err error) {
	if timeArg == "" {
		return "00", "00", "00", nil
	}

	parts := strings.Split(timeArg, ":")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("time must be in HH:MM:SS format (e.g., 10:00:00)")
	}

	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return "", "", "", fmt.Errorf("invalid hour (must be 00-23)")
	}
	hour = fmt.Sprintf("%02d", h)

	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return "", "", "", fmt.Errorf("invalid minute (must be 00-59)")
	}
	minute = fmt.Sprintf("%02d", m)

	s, err := strconv.Atoi(parts[2])
	if err != nil || s < 0 || s > 59 {
		return "", "", "", fmt.Errorf("invalid second (must be 00-59)")
	}
	second = fmt.Sprintf("%02d", s)

	return hour, minute, second, nil
}

func (d *Daemon) Start() error {
	pidFilePath, err := getPidFilePath()
	if err != nil {
		return fmt.Errorf("failed to get PID file path: %w", err)
	}

	cntxt := &daemon.Context{
		PidFileName: pidFilePath,
		PidFilePerm: 0644,
		WorkDir:     "./",
		Umask:       027,
	}

	child, err := cntxt.Reborn()
	if err != nil {
		return errors.New("failed to start daemon: try 'cron --status'")
	}
	if child != nil {
		fmt.Println("Daemon started. Use '--status' flag to check the state.")
		return nil
	}
	defer cntxt.Release()

	if err := d.saveState(true); err != nil {
		return fmt.Errorf("failed to save initial state: %w", err)
	}

	return d.run()
}

func (d *Daemon) run() error {
	c := cron.New(cron.WithLocation(location))

	_, err := c.AddFunc(d.interval, func() {
		d.executeTask()
	})

	if err != nil {
		return fmt.Errorf("failed to add cron job: %w", err)
	}

	c.Start()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	<-sig

	c.Stop()

	if err := d.saveState(false); err != nil {
		fmt.Printf("Failed to save final state: %v\n", err)
	}

	return nil
}

func (d *Daemon) executeTask() {
	now := time.Now()

	// Create pipes for stdout and stderr
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stdout = w
	os.Stderr = w

	// Save the original logger settings
	oldLogger := log.Default()
	// Set logger output to the pipe
	log.SetOutput(w)

	// Call the execute function
	d.executeFunc()

	// Restore original stdout, stderr and logger
	w.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	log.SetOutput(oldLogger.Writer())

	// Read the captured output
	output, _ := io.ReadAll(r)

	// Create log with timestamp, interval, and captured output
	logMsg := fmt.Sprintf("Task executed at: %v\n\nOutput:\n%s",
		now.Format(time.RFC3339),
		string(output))
	d.createExecutionLog(logMsg)

	// Update state
	if err := d.saveState(true); err != nil {
		fmt.Printf("Failed to update state: %v\n", err)
	}
}

func (d *Daemon) createExecutionLog(message string) error {
	logsDir, err := getLogsDir()
	if err != nil {
		return fmt.Errorf("failed to get logs directory: %w", err)
	}

	now := time.Now()
	logFileName := fmt.Sprintf("execution_%s.log", now.Format("2006-01-02T15-04-05"))
	logFilePath := filepath.Join(logsDir, logFileName)

	logContent := fmt.Sprintf("%s\n", message)

	err = os.WriteFile(logFilePath, []byte(logContent), 0644)
	if err != nil {
		return fmt.Errorf("failed to write execution log: %w", err)
	}

	return nil
}

func (d *Daemon) saveState(isRunning bool) error {
	state := ExecutionState{
		LastExecuted: time.Now(),
		IsRunning:    isRunning,
		Interval:     d.interval,
	}

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	stateFilePath, err := getStateFilePath()
	if err != nil {
		return err
	}

	return os.WriteFile(stateFilePath, data, 0644)
}

// Helper functions for directory and file paths
func getLogsDir() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	return filepath.Join(currentDir, logsDirName), nil
}

func getPidFilePath() (string, error) {
	logsDir, err := getLogsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(logsDir, pidFileName), nil
}

func getStateFilePath() (string, error) {
	logsDir, err := getLogsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(logsDir, stateFileName), nil
}

func ensureLogsDirectory() error {
	logsDir, err := getLogsDir()
	if err != nil {
		return err
	}

	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		err = os.MkdirAll(logsDir, 0755)
		if err != nil {
			return fmt.Errorf("failed to create logs directory: %w", err)
		}
	}
	return nil
}

// Public functions for CLI commands
func CheckStatus() error {
	pidFilePath, err := getPidFilePath()
	if err != nil {
		return fmt.Errorf("error getting PID file path: %w", err)
	}

	_, err = os.Stat(pidFilePath)
	if os.IsNotExist(err) {
		fmt.Println("Status: Scheduled execution is not set")
		return nil
	}

	stateFilePath, err := getStateFilePath()
	if err != nil {
		return fmt.Errorf("error getting state file path: %w", err)
	}

	data, err := os.ReadFile(stateFilePath)
	if err != nil {
		return fmt.Errorf("error reading state file: %w", err)
	}

	var state ExecutionState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("error parsing state file: %w", err)
	}

	fmt.Printf("Status: %v\n", map[bool]string{true: "Running", false: "Stopped"}[state.IsRunning])
	fmt.Printf("Last executed: %v\n", state.LastExecuted.In(location).Format(time.RFC3339))
	fmt.Printf("Timezone: %v\n", location.String())

	return nil
}

func StopDaemon() error {
	pidFilePath, err := getPidFilePath()
	if err != nil {
		return fmt.Errorf("error getting PID file path: %w", err)
	}

	pidData, err := os.ReadFile(pidFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Scheduled execution is not set")
			return nil
		}
		return fmt.Errorf("error reading PID file: %w", err)
	}

	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		return fmt.Errorf("invalid PID in file: %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("error finding process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("error stopping daemon: %w", err)
	}

	// Wait for process to terminate
	time.Sleep(time.Second)

	// Delete state file
	stateFilePath, err := getStateFilePath()
	if err != nil {
		return fmt.Errorf("error getting state file path: %w", err)
	}
	if err := os.Remove(stateFilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error removing state file: %w", err)
	}

	// Delete PID file
	if err := os.Remove(pidFilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error removing PID file: %w", err)
	}

	fmt.Println("Daemon stopped successfully")
	return nil
}
