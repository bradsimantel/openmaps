// Package supervisor observes one owned process group and terminates it when
// sampled resource budgets are exceeded. Sampling cannot enforce hard RSS limits.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Options struct {
	Root, Report, ReportDir string
	Samples, TemporaryPath  string
	RSSMiB, ReserveGiB      int64
	Command                 []string
	Stdout, Stderr          io.Writer
	Interval                time.Duration
	Free                    func(string) (int64, error)
	RSS                     func(context.Context, int) (int64, error)
}
type Report struct {
	Command    []string         `json:"command"`
	Seconds    float64          `json:"seconds"`
	Peak       int64            `json:"sampled_peak_rss_bytes"`
	Abort      string           `json:"abort_reason,omitempty"`
	ReturnCode int              `json:"returncode"`
	FreeBefore int64            `json:"free_before"`
	MinFree    int64            `json:"minimum_free_bytes"`
	FreeAfter  int64            `json:"free_after"`
	Budgets    map[string]int64 `json:"budgets"`
	MaxRSS     int64            `json:"child_maxrss_native_units"`
	PeakAnon   int64            `json:"sampled_peak_anonymous_rss_bytes,omitempty"`
	PeakFile   int64            `json:"sampled_peak_file_rss_bytes,omitempty"`
	PeakTemp   int64            `json:"sampled_peak_temporary_disk_bytes,omitempty"`
	ReadBytes  int64            `json:"process_tree_read_bytes,omitempty"`
	WriteBytes int64            `json:"process_tree_write_bytes,omitempty"`
	CPUSeconds float64          `json:"process_tree_cpu_seconds,omitempty"`
	PeakCPU    float64          `json:"sampled_peak_cpu_percent,omitempty"`
	SampleMS   int64            `json:"sample_interval_milliseconds,omitempty"`
	Note       string           `json:"note"`
}

func freeDisk(path string) (int64, error) {
	var st syscall.Statfs_t
	e := syscall.Statfs(path, &st)
	return int64(st.Bavail) * int64(st.Bsize), e
}
func sampleRSS(ctx context.Context, pid int) (int64, error) {
	query, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, e := exec.CommandContext(query, "ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if e != nil {
		return 0, e
	}
	n, e := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n * 1024, e
}
func Run(ctx context.Context, o Options) (report Report, err error) {
	report = Report{Command: o.Command, ReturnCode: -1, Budgets: map[string]int64{"rss_mib": o.RSSMiB, "reserve_gib": o.ReserveGiB}, Note: "Sampled process-tree RSS is an external one-second observation, not a hard allocator or total OS-cache bound. Child maximum RSS uses platform-native units."}
	if len(o.Command) == 0 || o.RSSMiB < 1 || o.RSSMiB > 65536 || o.ReserveGiB < 32 || o.ReserveGiB > 1024 {
		return report, errors.New("command and valid headroom budgets required")
	}
	if (o.Report == "") == (o.ReportDir == "") {
		return report, errors.New("choose exactly one report filename or report directory")
	}
	if o.Free == nil {
		o.Free = freeDisk
	}
	if o.RSS == nil {
		o.RSS = sampleRSS
	}
	if o.Interval <= 0 {
		o.Interval = time.Second
	}
	report.SampleMS = o.Interval.Milliseconds()
	var e error
	var samples *os.File
	if o.Samples != "" {
		samples, e = os.OpenFile(o.Samples, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return report, e
		}
		defer func() { err = errors.Join(err, samples.Sync(), samples.Close()) }()
	}
	var f *os.File
	if o.ReportDir != "" {
		// Service restarts retain a distinct report for every supervised lifetime.
		f, e = os.CreateTemp(o.ReportDir, "resources-"+time.Now().UTC().Format("20060102T150405Z")+"-*.json")
	} else {
		f, e = os.OpenFile(o.Report, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	}
	if e != nil {
		return report, e
	}
	start := time.Now()
	defer func() {
		report.Seconds = time.Since(start).Seconds()
		free, e := o.Free(o.Root)
		report.FreeAfter = free
		err = errors.Join(err, e)
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		err = errors.Join(err, enc.Encode(report), f.Sync(), f.Close())
	}()
	report.FreeBefore, e = o.Free(o.Root)
	if e != nil {
		return report, e
	}
	if report.FreeBefore < o.ReserveGiB<<30 {
		report.Abort = "disk reserve already crossed"
		return report, errors.New(report.Abort)
	}
	report.MinFree = report.FreeBefore
	child := exec.Command(o.Command[0], o.Command[1:]...)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	child.Stdout = o.Stdout
	child.Stderr = o.Stderr
	child.Stdin = os.Stdin
	if e = child.Start(); e != nil {
		return report, e
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()
	finish := func(e error) error {
		report.ReturnCode = child.ProcessState.ExitCode()
		if usage, ok := child.ProcessState.SysUsage().(*syscall.Rusage); ok {
			report.MaxRSS = usage.Maxrss
		}
		return e
	}
	terminate := func() error {
		_ = syscall.Kill(-child.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case e := <-done:
			return finish(e)
		case <-timer.C:
			_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
			return finish(<-done)
		}
	}
	var previousCPU float64
	sampleDetailedMetrics := func() {
		detailed, sampleErr := detailedProcessTree(child.Process.Pid, o.TemporaryPath)
		if sampleErr != nil {
			return
		}
		report.Peak = max(report.Peak, detailed.RSSBytes)
		report.PeakAnon = max(report.PeakAnon, detailed.AnonymousRSSBytes)
		report.PeakFile = max(report.PeakFile, detailed.FileRSSBytes)
		report.PeakTemp = max(report.PeakTemp, detailed.TemporaryBytes)
		report.ReadBytes = max(report.ReadBytes, detailed.ReadBytes)
		report.WriteBytes = max(report.WriteBytes, detailed.WriteBytes)
		report.CPUSeconds = max(report.CPUSeconds, detailed.CPUSeconds)
		if previousCPU != 0 {
			report.PeakCPU = max(report.PeakCPU, 100*(detailed.CPUSeconds-previousCPU)/o.Interval.Seconds())
		}
		previousCPU = detailed.CPUSeconds
		if samples != nil {
			detailed.ElapsedSeconds = time.Since(start).Seconds()
			_ = json.NewEncoder(samples).Encode(detailed)
		}
	}
	for {
		select {
		case e := <-done:
			return report, finish(e)
		case <-ctx.Done():
			report.Abort = "supervisor cancelled"
			return report, errors.Join(ctx.Err(), terminate())
		case <-ticker.C:
			sampleDetailedMetrics()
			rss, sampleError := o.RSS(ctx, child.Process.Pid)
			if sampleError != nil {
				select {
				case e := <-done:
					return report, finish(e)
				default:
				}
				report.Abort = "RSS observation failed: " + sampleError.Error()
			} else {
				report.Peak = max(report.Peak, rss)
				if rss > o.RSSMiB<<20 {
					report.Abort = "sampled RSS exceeded budget"
				}
			}
			free, e := o.Free(o.Root)
			if e != nil {
				report.Abort = "disk observation failed: " + e.Error()
			} else if free < o.ReserveGiB<<30 {
				report.Abort = "disk reserve crossed"
			}
			if e == nil && free < report.MinFree {
				report.MinFree = free
			}
			if report.Abort != "" {
				return report, errors.Join(fmt.Errorf("%s", report.Abort), terminate())
			}
		}
	}
}
