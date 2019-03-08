// Copyright 2019 Keybase, Inc. All rights reserved. Use of
// this source code is governed by the included BSD license.

package logger

import (
	"bufio"
	"io"
	"sync"
	"time"
)

type autoFlushingBufferedWriter struct {
	lock           sync.RWMutex
	bufferedWriter *bufio.Writer
	backupWriter   *bufio.Writer

	frequency        time.Duration
	timer            *time.Timer
	currentlyWaiting bool
	shutdown         chan struct{}
}

var _ io.Writer = &autoFlushingBufferedWriter{}

func (writer *autoFlushingBufferedWriter) backgroundFlush() {
	for {
		select {
		case <-writer.timer.C:
			// Swap out active and backup writers
			writer.lock.Lock()
			writer.currentlyWaiting = false
			previouslyActive := writer.bufferedWriter
			writer.bufferedWriter = writer.backupWriter
			writer.lock.Unlock()

			// Flush the previously active writer and complete the swap. We do
			// not need to complete the swap under lock because this is the
			// only goroutine accessing backupWriter.
			writer.backupWriter = previouslyActive
			writer.backupWriter.Flush()
		case <-writer.shutdown:
			writer.timer.Stop()
			writer.bufferedWriter.Flush()
			return
		}
	}
}

// NewAutoFlushingBufferedWriter returns an io.Writer that buffers its output
// and flushes automatically after `flushFrequency`.
func NewAutoFlushingBufferedWriter(baseWriter io.Writer,
	flushFrequency time.Duration) (w io.Writer, shutdown chan struct{}) {
	result := &autoFlushingBufferedWriter{
		bufferedWriter: bufio.NewWriter(baseWriter),
		backupWriter:   bufio.NewWriter(baseWriter),
		frequency:      flushFrequency,
		timer:          time.NewTimer(flushFrequency),
	}
	go result.backgroundFlush()
	return result, result.shutdown
}

func (writer *autoFlushingBufferedWriter) Write(p []byte) (int, error) {
	writer.lock.RLock()
	defer writer.lock.RUnlock()
	n, err := writer.bufferedWriter.Write(p)
	if err != nil {
		return n, err
	}
	if writer.currentlyWaiting {
		return n, nil
	}
	// Docs say this should not be called concurrently with channel receive.
	// I've read the internal code and the only 2 ways it could resolve are
	// that the timer is stopped when it has not yet fired, or it fires and
	// then is reset. In the latter case, we get slightly more frequent
	// flushing than we need; in the former, we get a slight delay. This race
	// is unlikely, so it's exceedingly unlikely that the delay will accumulate
	// over more than one successive race.
	writer.timer.Reset(writer.frequency)
	// Any race over `currentlyWaiting` will have minimal impact. The only race
	// possible given the locking is between two copies of the below line
	// both trying to set the value to `true` simultaneously.
	writer.currentlyWaiting = true

	return n, nil
}
