package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// CronLock prevents multiple concurrent sessions from double-firing cron jobs.
type CronLock struct {
	lockPath string
}

func NewCronLock(lockPath string) *CronLock {
	return &CronLock{lockPath: lockPath}
}

// Acquire tries to acquire the cron lock. Returns true on success.
func (cl *CronLock) Acquire() bool {
	if _, err := os.Stat(cl.lockPath); err == nil {
		// Lock file exists, read PID
		data, err := os.ReadFile(cl.lockPath)
		if err == nil {
			storedPid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				if isPIDAlive(storedPid) {
					return false
				}
			}
		}
		// Stale lock -- remove it
		_ = os.Remove(cl.lockPath)
	}

	_ = os.MkdirAll(filepath.Dir(cl.lockPath), 0755)
	err := os.WriteFile(cl.lockPath, []byte(strconv.Itoa(os.Getpid())), 0644)
	return err == nil
}

// Release deletes the lock file if it belongs to this process.
func (cl *CronLock) Release() {
	if _, err := os.Stat(cl.lockPath); err == nil {
		data, err := os.ReadFile(cl.lockPath)
		if err == nil {
			storedPid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && storedPid == os.Getpid() {
				_ = os.Remove(cl.lockPath)
			}
		}
	}
}

func isPIDAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if err == syscall.ESRCH {
		return false
	}
	if err == os.ErrProcessDone {
		return false
	}
	return true
}

// cronMatches checks if the 5-field cron expression matches a given time.
func cronMatches(expr string, dt time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}

	values := []int{dt.Minute(), dt.Hour(), dt.Day(), int(dt.Month()), int(dt.Weekday())}
	ranges := [][]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

	for i := 0; i < 5; i++ {
		if !fieldMatches(fields[i], values[i], ranges[i][0], ranges[i][1]) {
			return false
		}
	}
	return true
}

func fieldMatches(field string, value int, lo, hi int) bool {
	if field == "*" {
		return true
	}

	parts := strings.Split(field, ",")
	for _, part := range parts {
		step := 1
		if strings.Contains(part, "/") {
			sp := strings.Split(part, "/")
			part = sp[0]
			step, _ = strconv.Atoi(sp[1])
			if step <= 0 {
				step = 1
			}
		}

		if part == "*" {
			if (value-lo)%step == 0 {
				return true
			}
		} else if strings.Contains(part, "-") {
			sp := strings.Split(part, "-")
			if len(sp) == 2 {
				start, _ := strconv.Atoi(sp[0])
				end, _ := strconv.Atoi(sp[1])
				if value >= start && value <= end && (value-start)%step == 0 {
					return true
				}
			}
		} else {
			val, err := strconv.Atoi(part)
			if err == nil {
				// Special case: Sunday in cron dow field can be 0 or 7.
				if lo == 0 && hi == 6 && value == 0 && val == 7 {
					return true
				}
				if val == value {
					return true
				}
			}
		}
	}

	return false
}

func hashString(s string) int32 {
	var h int32
	for _, c := range s {
		h = 31*h + c
	}
	return h
}
