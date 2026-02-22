// Package main provides a demo image generator for testing the SFPG gallery application.
//
// This program downloads images from LoremFlickr (https://loremflickr.com) and organizes
// them into a hierarchical folder structure suitable for testing a photo gallery application.
// It uses a worker pool pattern for concurrent downloads with retry logic and idempotency
// support to allow resuming interrupted downloads.
//
// Usage:
//
//	go run cmd/gendemoimage/main.go
//
// The program creates images in ./tmp/Images/ with the following structure:
//
//	./tmp/Images/
//	├── landscapes/
//	│   └── batch_100/
//	│       ├── img_0000.jpg
//	│       └── ...
//	├── architecture/
//	├── mechanical/
//	└── wildlife/
//
// Idempotency: The program can be safely re-run. Existing files with content are skipped,
// allowing for resume capability if the process is interrupted.
package main

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// TotalImages is the total number of images to download.
	TotalImages = 5000

	// MinPerFolder is the minimum number of images per batch folder.
	MinPerFolder = 5

	// MaxPerFolder is the maximum number of images per batch folder.
	MaxPerFolder = 200

	// FixedSeed ensures reproducible directory structures across runs.
	FixedSeed = 43

	// BaseDir is the root directory for all generated images.
	BaseDir = "./tmp/Images"

	// MaxWorkers is the number of concurrent download workers.
	MaxWorkers = 12

	// MaxRetries is the maximum number of retry attempts for failed downloads.
	MaxRetries = 5
)

// Job represents a single image download task.
//
// It contains the image index for URL generation, the search keyword
// for LoremFlickr, and the target filesystem path for saving the image.
type Job struct {
	// Index is the unique sequential identifier for this image.
	// Used with the lock parameter to ensure unique images.
	Index int

	// Keyword is the search term passed to LoremFlickr's image API.
	// Determines the subject matter of the downloaded image.
	Keyword string

	// Path is the absolute filesystem path where the image will be saved.
	Path string
}

// main is the entry point for the demo image generator.
//
// It orchestrates the download process by:
// 1. Initializing a random number generator with a fixed seed for reproducibility
// 2. Creating a buffered job channel and launching worker goroutines
// 3. Generating download jobs distributed across categories and batch folders
// 4. Waiting for all workers to complete their assigned jobs
//
// The program creates a hierarchical folder structure under BaseDir with images
// organized by category (landscapes, architecture, mechanical, wildlife) and batch ID.
func main() {
	rng := rand.New(rand.NewSource(FixedSeed))
	categories := []string{"landscapes", "architecture", "mechanical", "wildlife"}
	subCats := map[string][]string{
		"landscapes":   {"nature", "mountains", "forest"},
		"architecture": {"building", "skyscraper", "bridge"},
		"mechanical":   {"engine", "gears", "industrial"},
		"wildlife":     {"wolf", "eagle", "deer"},
	}

	jobs := make(chan Job, TotalImages)
	var wg sync.WaitGroup

	for w := 1; w <= MaxWorkers; w++ {
		wg.Add(1)
		go worker(jobs, &wg)
	}

	imgCount := 0
	batchID := 100
	for imgCount < TotalImages {
		cat := categories[rng.Intn(len(categories))]
		sub := subCats[cat][rng.Intn(len(subCats[cat]))]

		folder := filepath.Join(BaseDir, cat, fmt.Sprintf("batch_%d", batchID))
		os.MkdirAll(folder, 0755)

		numInFolder := rng.Intn(MaxPerFolder-MinPerFolder+1) + MinPerFolder
		if numInFolder > (TotalImages - imgCount) {
			numInFolder = TotalImages - imgCount
		}

		for i := 0; i < numInFolder; i++ {
			jobs <- Job{
				Index:   imgCount,
				Keyword: sub,
				Path:    filepath.Join(folder, fmt.Sprintf("img_%04d.jpg", imgCount)),
			}
			imgCount++
		}
		batchID++
	}

	close(jobs)
	wg.Wait()
	fmt.Println("\nProcess complete.")
}

// worker processes download jobs from the job channel until the channel is closed.
//
// Each worker maintains its own HTTP client with a 30-second timeout and processes
// jobs sequentially. The worker implements idempotency by checking if the target file
// already exists and has content before attempting download.
//
// Jobs that fail after MaxRetries attempts are logged as fatal errors but do not
// halt the overall process, allowing other workers to continue.
//
// The wg WaitGroup is marked as done when the worker exits, allowing the main
// goroutine to detect when all workers have completed.
//
// Parameters:
//
//	jobs - A read-only channel of Job structs to process
//	wg   - Pointer to WaitGroup for signaling completion
func worker(jobs <-chan Job, wg *sync.WaitGroup) {
	defer wg.Done()
	client := &http.Client{Timeout: 30 * time.Second}

	for job := range jobs {
		// Restartability: Check if file exists and has content
		if info, err := os.Stat(job.Path); err == nil && info.Size() > 0 {
			continue
		}

		success := false
		backoff := 2 * time.Second

		for range MaxRetries {
			err := download(client, job)
			if err == nil {
				success = true
				break
			}
			time.Sleep(backoff)
			backoff *= 2
		}

		if !success {
			fmt.Printf("\n[FATAL] Failed image %d\n", job.Index)
		}
	}
}

// download fetches a single image from LoremFlickr and saves it to the specified path.
//
// The function constructs a URL using LoremFlickr's API with the job's keyword and
// a unique lock parameter (the image index) to ensure different images are returned
// even for the same keyword.
//
// The HTTP response body is streamed directly to the file without buffering in memory,
// making this function suitable for downloading large numbers of images efficiently.
//
// Parameters:
//
//	client - HTTP client to use for the request (enables connection reuse and timeout control)
//	job    - Job struct containing the keyword for image selection and target path
//
// Returns:
//
//	error - Any error encountered during HTTP request, response validation, or file writing
//
// URL format: https://loremflickr.com/1280/720/{keyword}?lock={index}
//
// Example:
//
//	download(client, Job{Index: 42, Keyword: "wolf", Path: "/path/to/img_0042.jpg"})
//	// Fetches: https://loremflickr.com/1280/720/wolf?lock=42
func download(client *http.Client, job Job) error {
	url := fmt.Sprintf("https://loremflickr.com/1280/720/%s?lock=%d", job.Keyword, job.Index)

	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(job.Path)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
