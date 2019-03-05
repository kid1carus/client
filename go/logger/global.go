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

func getBufferedErrorWriter() io.Writer {
	writer := ErrorWriter()
	buf := bufio.NewWriter(writer)
	go func() {
		for range time.Tick(300 * time.Millisecond) {
			// TODO: do we care if this errors
			buf.Flush()
		}
	}()
	return buf
}

func init() {
	// TODO: I am not sure how color gets set on the logger,
	//  but it's not logging in color anymore sometimes
	logBackend := logging.NewLogBackend(getBufferedErrorWriter(), "", 0)
	logging.SetBackend(logBackend)
}
