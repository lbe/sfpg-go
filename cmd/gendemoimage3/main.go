package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	TotalImages  = 5000
	BaseDir      = "./tmp/test_gallery"
	ManifestFile = "./tmp/test_gallery/manifest.json"
	MaxWorkers   = 5 // Lowered further to prevent 429s
	FixedSeed    = 42
	UserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	MaxFileSize  = 10 * 1024 * 1024
)

type MediaFile struct {
	URL      string `json:"url"`
	Category string `json:"category"`
}

type Job struct {
	Index int
	File  MediaFile
	Path  string
}

func main() {
	if err := os.MkdirAll(BaseDir, 0755); err != nil {
		fmt.Printf("Fatal: %v\n", err)
		return
	}

	manifest := buildManifestRestartable()
	if len(manifest) == 0 {
		return
	}

	rng := rand.New(rand.NewSource(FixedSeed))
	jobs := make(chan Job, len(manifest))
	var wg sync.WaitGroup

	for i := 0; i < MaxWorkers; i++ {
		wg.Add(1)
		go worker(jobs, &wg, len(manifest))
	}

	imgCount := 0
	batchID := 100
	for imgCount < len(manifest) {
		cat := manifest[imgCount].Category
		folder := filepath.Join(BaseDir, cat, fmt.Sprintf("batch_%d", batchID))
		_ = os.MkdirAll(folder, 0755)
		numInFolder := rng.Intn(195) + 5
		for i := 0; i < numInFolder && imgCount < len(manifest); i++ {
			jobs <- Job{Index: imgCount, File: manifest[imgCount], Path: filepath.Join(folder, fmt.Sprintf("img_%04d.jpg", imgCount))}
			imgCount++
		}
		batchID++
	}

	close(jobs)
	wg.Wait()
}

func worker(jobs <-chan Job, wg *sync.WaitGroup, total int) {
	defer wg.Done()
	client := &http.Client{Timeout: 60 * time.Second}

	for j := range jobs {
		if info, err := os.Stat(j.Path); err == nil && info.Size() > 0 {
			continue
		}

		// Throttle slightly to respect CDN
		time.Sleep(100 * time.Millisecond)

		success := false
		for attempt := 0; attempt < 5; attempt++ {
			req, _ := http.NewRequest("GET", j.File.URL, nil)
			req.Header.Set("User-Agent", UserAgent)

			resp, err := client.Do(req)
			if err != nil || resp == nil {
				time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
				continue
			}

			if resp.StatusCode == 429 {
				resp.Body.Close()
				// Exponential backoff for rate limits
				time.Sleep(time.Duration(math.Pow(2, float64(attempt+1))) * time.Second)
				continue
			}

			if resp.StatusCode != 200 {
				resp.Body.Close()
				break
			}

			f, err := os.Create(j.Path)
			if err == nil {
				_, _ = io.Copy(f, resp.Body)
				f.Close()
				success = true
			}
			resp.Body.Close()
			if success {
				break
			}
		}

		if j.Index%10 == 0 {
			fmt.Printf("\rProgress: %d/%d", j.Index+1, total)
		}
	}
}

func buildManifestRestartable() []MediaFile {
	manifest, _ := loadManifest()
	if len(manifest) >= TotalImages {
		return manifest
	}

	cats := []string{"Landscape_photography", "Architecture", "Machines", "Animals"}
	client := &http.Client{Timeout: 45 * time.Second}

	for _, sc := range cats {
		if len(manifest) >= TotalImages {
			break
		}
		cont := ""
		for len(manifest) < TotalImages {
			u := fmt.Sprintf("https://commons.wikimedia.org/w/api.php?action=query&list=categorymembers&cmtitle=Category:%s&cmtype=file&cmlimit=100&format=json&cmcontinue=%s", sc, url.QueryEscape(cont))
			req, _ := http.NewRequest("GET", u, nil)
			req.Header.Set("User-Agent", UserAgent)
			resp, err := client.Do(req)
			if err != nil || resp == nil {
				break
			}

			var res struct {
				Continue struct{ CMContinue string }
				Query    struct{ CategoryMembers []struct{ Title string } }
			}
			json.NewDecoder(resp.Body).Decode(&res)
			resp.Body.Close()

			var titles []string
			for _, m := range res.Query.CategoryMembers {
				lt := strings.ToLower(m.Title)
				if strings.HasSuffix(lt, ".jpg") || strings.HasSuffix(lt, ".jpeg") {
					titles = append(titles, m.Title)
				}
			}

			const subBatchSize = 10
			for i := 0; i < len(titles); i += subBatchSize {
				end := i + subBatchSize
				if end > len(titles) {
					end = len(titles)
				}
				batch := titles[i:end]
				iURL := fmt.Sprintf("https://commons.wikimedia.org/w/api.php?action=query&titles=%s&prop=imageinfo&iiprop=url|size|commonmetadata&format=json", url.QueryEscape(strings.Join(batch, "|")))
				reqI, _ := http.NewRequest("GET", iURL, nil)
				reqI.Header.Set("User-Agent", UserAgent)
				respI, err := client.Do(reqI)
				if err != nil || respI == nil {
					continue
				}

				var iRes struct {
					Query struct {
						Pages map[string]struct {
							ImageInfo []struct {
								URL      string                  `json:"url"`
								Size     int64                   `json:"size"`
								Metadata []struct{ Name string } `json:"commonmetadata"`
							} `json:"imageinfo"`
						} `json:"pages"`
					} `json:"query"`
				}
				json.NewDecoder(respI.Body).Decode(&iRes)
				respI.Body.Close()

				for _, p := range iRes.Query.Pages {
					if len(p.ImageInfo) > 0 {
						info := p.ImageInfo[0]
						hasGPS := false
						for _, m := range info.Metadata {
							if m.Name == "GPSLatitude" {
								hasGPS = true
								break
							}
						}
						if hasGPS && info.Size > 0 && info.Size <= MaxFileSize {
							manifest = append(manifest, MediaFile{URL: info.URL, Category: sc})
						}
					}
					if len(manifest) >= TotalImages {
						break
					}
				}
			}
			saveManifest(manifest)
			if res.Continue.CMContinue == "" {
				break
			}
			cont = res.Continue.CMContinue
		}
	}
	return manifest
}

func loadManifest() ([]MediaFile, error) {
	d, err := os.ReadFile(ManifestFile)
	if err != nil {
		return nil, err
	}
	var m []MediaFile
	return m, json.Unmarshal(d, &m)
}

func saveManifest(m []MediaFile) {
	d, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(ManifestFile, d, 0644)
}
