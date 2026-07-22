// Package jsonl reads newline-delimited sources line by line with a
// size ceiling that SKIPS oversized lines instead of aborting the whole
// source — one pathological record must never cost the file's valid
// ones. Memory stays bounded by the ceiling, not the file.
package jsonl

import (
	"bufio"
	"bytes"
	"io"
)

// Scan iterates the complete lines of r in order. Each line (without
// its trailing newline) goes to fn with its 1-based line number; a line
// longer than max bytes is consumed but not buffered, and reported to
// over with its size instead. The first error from fn or over stops the
// scan and is returned.
func Scan(r io.Reader, max int, fn func(lineNo int, line []byte) error, over func(lineNo int, size int64) error) error {
	br := bufio.NewReaderSize(r, 64*1024)
	lineNo := 0
	for {
		line, size, tooLong, err := next(br, max)
		if size == 0 {
			if err == io.EOF || err == nil {
				return nil
			}
			return err
		}
		lineNo++
		if tooLong {
			if oerr := over(lineNo, size); oerr != nil {
				return oerr
			}
		} else if cerr := fn(lineNo, line); cerr != nil {
			return cerr
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// next reads one line, consuming past max without buffering.
func next(br *bufio.Reader, max int) (line []byte, size int64, tooLong bool, err error) {
	for {
		frag, rerr := br.ReadSlice('\n')
		size += int64(len(frag))
		if !tooLong {
			line = append(line, frag...)
			if len(line) > max {
				tooLong = true
				line = nil
			}
		}
		switch rerr {
		case bufio.ErrBufferFull:
			continue
		case nil:
			return bytes.TrimRight(line, "\r\n"), size, tooLong, nil
		default:
			return bytes.TrimRight(line, "\r\n"), size, tooLong, rerr
		}
	}
}
