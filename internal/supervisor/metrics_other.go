//go:build !linux

package supervisor

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

func detailedProcessTree(int, string) (DetailedSample, error) { return DetailedSample{}, nil }
