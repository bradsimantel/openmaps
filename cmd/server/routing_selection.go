package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"openmaps/internal/routing"
)

func watchRoutingSelection(ctx context.Context, c *routing.Service, path string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last string
	var lastError string
	failure := func(err error) {
		last = ""
		c.RecordSelectionError(err)
		if err.Error() != lastError {
			log.Printf("Routing selection: %v", err)
			lastError = err.Error()
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f, err := os.Open(path)
			if err != nil {
				failure(err)
				continue
			}
			b, err := io.ReadAll(io.LimitReader(f, 65537))
			f.Close()
			if err != nil || len(b) > 65536 {
				failure(errors.New("routing selection unreadable or oversized"))
				continue
			}
			if string(b) == last {
				continue
			}
			last = string(b)
			var selection struct {
				Directory string `json:"directory"`
			}
			if err := json.Unmarshal(b, &selection); err != nil || selection.Directory == "" {
				failure(errors.New("routing selection requires directory"))
				continue
			}
			dir := selection.Directory
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(filepath.Dir(path), dir)
			}
			dir, err = filepath.Abs(dir)
			if err != nil {
				failure(err)
				continue
			}
			current, _ := c.Status()
			if dir == current.Directory {
				c.RecordSelectionError(nil)
				lastError = ""
				continue
			}
			started := time.Now()
			if err := c.Replace(ctx, dir); err != nil {
				log.Printf("Routing snapshot replacement rejected; old snapshot retained: %v", err)
			} else {
				lastError = ""
				log.Printf("Routing snapshot replaced: %s (load and retirement %s)", dir, time.Since(started))
			}
		}
	}
}
