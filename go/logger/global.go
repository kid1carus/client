package logger

import (
	"bufio"
	"io"
	"os"
	"sync"
	"time"

	logging "github.com/keybase/go-logging"
	isatty "github.com/mattn/go-isatty"
)

var globalLock sync.Mutex
var stderrIsTerminal = isatty.IsTerminal(os.Stderr.Fd())
var currentLogFileWriter *LogFileWriter
var stdErrLoggingShutdown chan<- struct{}

func bufferLogs(writer io.Writer) (io.Writer, chan<- struct{}) {
	buf := bufio.NewWriter(writer)
	shutdown := make(chan struct{})
	go func() {
		t := time.NewTicker(300 * time.Millisecond)
		for {
			select {
			case <-t.C:
				buf.Flush()
			case <-shutdown:
				buf.Flush()
				t.Stop()
				return
			}
		}
	}()
	return buf, shutdown
}

func init() {
	writer, shutdown := bufferLogs(ErrorWriter())
	stdErrLoggingShutdown = shutdown
	logBackend := logging.NewLogBackend(writer, "", 0)
	logging.SetBackend(logBackend)
}
