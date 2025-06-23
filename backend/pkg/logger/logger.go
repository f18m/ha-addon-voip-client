// This package is a tiny wrapper on top of standard log.Logger interface
// and creates logs that mimic the baresip logging style:
//
//	voip-client[PID]: <UnixEpoch> <Message>
//
// with the difference that the timestamp is not in a (hard to read) UnixEpoch;
// the result looks like:
package logger

import (
	"fmt"
	"log"
	"os"
	"time"
)

type LogLevel string

const (
	INFO  LogLevel = "INFO"
	WARN  LogLevel = "WARN"
	FATAL LogLevel = "FATAL"
)

type CustomLogger struct {
	logger *log.Logger
	pid    int
	prefix string
}

func NewCustomLogger(prefix string) *CustomLogger {
	pid := os.Getpid()
	logger := log.New(os.Stdout, "", 0) // No flags here, we'll add timestamp manually
	return &CustomLogger{
		logger: logger,
		pid:    pid,
		prefix: prefix,
	}
}

func (l *CustomLogger) Log(level LogLevel, message string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logMessage := fmt.Sprintf("%s[%d]: %s %s %s", l.prefix, l.pid, timestamp, level, message)
	l.logger.Print(logMessage)
}

// Info function used by GOBARESIP
func (l *CustomLogger) Info(args ...interface{}) {
	l.InfoPkg("go-baresip", fmt.Sprint(args...))
}

// Info function used by GOBARESIP
func (l *CustomLogger) Infof(template string, args ...interface{}) {
	l.InfoPkgf("go-baresip", template, args...)
}

// InfoPkg
// Prints at INFO level with a package prefix.
func (l *CustomLogger) InfoPkg(pkg, message string) {
	l.Log(INFO, pkg+": "+message)
}

// InfoPkgf
// Prints at INFO level with a package prefix.
// Arguments are handled in the manner of [fmt.Printf].
func (l *CustomLogger) InfoPkgf(pkg, format string, v ...any) {
	l.Log(INFO, pkg+": "+fmt.Sprintf(format, v...))
}

// Warn
func (l *CustomLogger) Warn(message string) {
	l.Log(WARN, message)
}

// Warnf
// Arguments are handled in the manner of [fmt.Printf].
func (l *CustomLogger) Warnf(format string, v ...any) {
	l.Warn(fmt.Sprintf(format, v...))
}

// Fatal
func (l *CustomLogger) Fatal(s string) {
	l.Log(FATAL, s)
}

// Fatal
// Arguments are handled in the manner of [fmt.Printf].
func (l *CustomLogger) Fatalf(format string, v ...any) {
	l.Fatal(fmt.Sprintf(format, v...))
}
