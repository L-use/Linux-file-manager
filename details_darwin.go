package main

import (
	"time"

	"golang.org/x/sys/unix"
)

func detailBirthTime(_ int, _ string, original unix.Stat_t) *int64 {
	if original.Btim.Sec == 0 && original.Btim.Nsec == 0 {
		return nil
	}
	value := time.Unix(original.Btim.Sec, original.Btim.Nsec).UnixMilli()
	return &value
}
