//go:build linux

package supervisor

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type DetailedSample struct {
	ElapsedSeconds    float64 `json:"elapsed_seconds"`
	Processes         int     `json:"processes"`
	RSSBytes          int64   `json:"rss_bytes"`
	AnonymousRSSBytes int64   `json:"anonymous_rss_bytes"`
	FileRSSBytes      int64   `json:"file_rss_bytes"`
	ReadBytes         int64   `json:"read_bytes"`
	WriteBytes        int64   `json:"write_bytes"`
	CPUSeconds        float64 `json:"cpu_seconds"`
	TemporaryBytes    int64   `json:"temporary_bytes"`
}

func detailedProcessTree(root int, temporaryPath string) (DetailedSample, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return DetailedSample{}, err
	}
	parents := map[int]int{}
	for _, entry := range entries {
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if readErr != nil {
			continue
		}
		end := strings.LastIndexByte(string(raw), ')')
		fields := strings.Fields(string(raw[end+1:]))
		if end < 0 || len(fields) < 15 {
			continue
		}
		parent, _ := strconv.Atoi(fields[1])
		parents[pid] = parent
	}
	isDescendant := func(pid int) bool {
		for pid > 1 {
			if pid == root {
				return true
			}
			next, ok := parents[pid]
			if !ok || next == pid {
				break
			}
			pid = next
		}
		return false
	}
	var sample DetailedSample
	for pid := range parents {
		if !isDescendant(pid) {
			continue
		}
		if err = addProcessMetrics(&sample, pid); err == nil {
			sample.Processes++
		}
	}
	if temporaryPath != "" {
		sample.TemporaryBytes, _ = directoryBytes(temporaryPath)
	}
	return sample, nil
}

func addProcessMetrics(sample *DetailedSample, pid int) error {
	file, err := os.Open(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(file)
	var rss, anonymous int64
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "Rss:":
			rss = value << 10
		case "Anonymous:":
			anonymous = value << 10
		}
	}
	file.Close()
	sample.RSSBytes += rss
	sample.AnonymousRSSBytes += anonymous
	sample.FileRSSBytes += max(int64(0), rss-anonymous)
	ioRaw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
	for _, line := range strings.Split(string(ioRaw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "read_bytes:":
			sample.ReadBytes += value
		case "write_bytes:":
			sample.WriteBytes += value
		}
	}
	statRaw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	end := strings.LastIndexByte(string(statRaw), ')')
	fields := strings.Fields(string(statRaw[end+1:]))
	if end >= 0 && len(fields) > 12 {
		user, _ := strconv.ParseFloat(fields[11], 64)
		system, _ := strconv.ParseFloat(fields[12], 64)
		sample.CPUSeconds += (user + system) / 100
	}
	return scanner.Err()
}

func directoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err == nil {
			total += info.Size()
		}
		return err
	})
	return total, err
}
