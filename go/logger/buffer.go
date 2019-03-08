// Copyright 2019 Keybase, Inc. All rights reserved. Use of
// this source code is governed by the included BSD license.

package logger

import (
	"bufio"
	"io"
	"time"
)

type autoFlushingBufferedWriter struct {
	bufferedWriter   *bufio.Writer
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
			// TODO: I claim this is concurrency safe, but how do I tell the race
			//  checker that?
			writer.currentlyWaiting = false
			writer.bufferedWriter.Flush()
		case <-writer.shutdown:
			writer.bufferedWriter.Flush()
			return
		}
	}
}

// NewAutoFlushingBufferedWriter returns an io.Writer that buffers its output
// and flushes automatically after `flushFrequency`.
func NewAutoFlushingBufferedWriter(baseWriter io.Writer,
	flushFrequency time.Duration) (w io.Writer, shutdown chan struct{}) {
	bufferedWriter := bufio.NewWriter(baseWriter)
	result := &autoFlushingBufferedWriter{
		bufferedWriter: bufferedWriter,
		frequency:      flushFrequency,
		timer:          time.NewTimer(flushFrequency),
	}
	go result.backgroundFlush()
	return result, result.shutdown
}

func (writer *autoFlushingBufferedWriter) Write(p []byte) (int, error) {
	n, err := writer.bufferedWriter.Write(p)
	if err != nil {
		return n, err
	}
	if writer.currentlyWaiting {
		return n, nil
	}
	// TODO: I think this is concurrency safe
	writer.timer.Reset(writer.frequency)
	writer.currentlyWaiting = true

	return n, nil
}
