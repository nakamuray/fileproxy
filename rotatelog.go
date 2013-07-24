package main

import (
	"log"
	"os"
	"strconv"
)

type RotateFile struct {
	FileName string
	Limit    int64
	Count    int
	file     *os.File
}

func NewRotateLog(filename string, limit int64, count int, prefix string, flag int) *log.Logger {
	return log.New(NewRotateFile(filename, limit, count), prefix, flag)
}

func NewRotateFile(filename string, limit int64, count int) *RotateFile {
	return &RotateFile{filename, limit, count, nil}
}

func (r *RotateFile) Write(p []byte) (int, error) {
	if r.file == nil {
		file, err := os.OpenFile(r.FileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return 0, err
		}
		r.file = file
	}

	info, err := r.file.Stat()
	if err != nil {
		return 0, err
	}

	if info.Size() > r.Limit {
		r.file.Close()
		r.file = nil

		err = r.rotate()

		if err != nil {
			return 0, err
		}

		return r.Write(p)
	}

	return r.file.Write(p)
}

func (r *RotateFile) rotate() error {
	for i := int64(r.Count); i > 0; i-- {
		newname := r.FileName + "." + strconv.FormatInt(i, 10)

		var oldname string
		if i == 1 {
			oldname = r.FileName
		} else {
			oldname = r.FileName + "." + strconv.FormatInt(i-1, 10)
		}

		// if oldname exists
		if _, err := os.Stat(oldname); err == nil {
			err = os.Rename(oldname, newname)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
