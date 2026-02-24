package main

import (
	"encoding/json"
	"fmt"
	"io"
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
	BaseDir      = "./test_gallery"
	ManifestFile = "manifest.json"
	MaxWorkers   = 12
	FixedSeed    = 42
	// IMPORTANT: Wikimedia WILL block empty/generic User-Agents.
	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) GalleryBot/1.0"
)

type MediaFile struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	Category string `json:"category"`
}

func main() {
	rng := rand.New(rand.NewSource(FixedSeed))

	manifest, err := loadManifest()
	if err != nil || len(manifest) == 0 {
		fmt.Println("No valid manifest found. Querying Wikimedia API...")
		manifest = buildManifest()
		if len(manifest) == 0 {
			fmt.Println("CRITICAL ERROR: No images found. Check network or User-Agent.")
			return
		}
		saveManifest(manifest)
	}

	fmt.Printf("Starting download of %d images...\n", len(manifest))
	jobs := make(chan struct {
		Index int
		File  MediaFile
		Path  string
	}, len(manifest))

	var wg sync.WaitGroup
	for w := 0; w < MaxWorkers; w++ {
		wg.Add(1)
		go worker(jobs, &wg)
	}

	imgCount := 0
	batchID := 100
	for imgCount < len(manifest) {
		file := manifest[imgCount]
		folder := filepath.Join(BaseDir, file.Category, fmt.Sprintf("batch_%d", batchID))
		os.MkdirAll(folder, 0755)

		numInFolder := rng.Intn(196) + 5
		for i := 0; i < numInFolder && imgCount < len(manifest); i++ {
			jobs <- struct {
				Index int
				File  MediaFile
				Path  string
			}{
				Index: imgCount,
				File:  manifest[imgCount],
				Path:  filepath.Join(folder, fmt.Sprintf("img_%04d.jpg", imgCount)),
			}
			imgCount++
		}
		batchID++
	}

	close(jobs)
	wg.Wait()
	fmt.Printf("\nFinished. Created %d files in %s\n", imgCount, BaseDir)
}

func worker(jobs <-chan struct {
	Index int
	File  MediaFile
	Path  string
}, wg *sync.WaitGroup) {
	defer wg.Done()
	client := &http.Client{Timeout: 45 * time.Second}
	for job := range jobs {
		if info, err := os.Stat(job.Path); err == nil && info.Size() > 0 {
			continue
		}

		req, _ := http.NewRequest("GET", job.File.URL, nil)
		req.Header.Set("User-Agent", UserAgent)

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("\n[Error] Network error on idx %d: %v", job.Index, err)
			continue
		}
		if resp.StatusCode != 200 {
			fmt.Printf("\n[Error] Status %d on idx %d", resp.StatusCode, job.Index)
			resp.Body.Close()
			continue
		}

		out, err := os.Create(job.Path)
		if err != nil {
			resp.Body.Close()
			continue
		}

		_, err = io.Copy(out, resp.Body)
		out.Close()
		resp.Body.Close()
		if job.Index%10 == 0 {
			fmt.Printf("\rDownloading: %d/%d", job.Index, TotalImages)
		}
	}
}

func buildManifest() []MediaFile {
	cats := []string{
		"Featured_pictures_of_landscapes",
		"Featured_pictures_of_architecture",
		"Featured_pictures_of_machinery",
		"Featured_pictures_of_animals",
	}

	var manifest []MediaFile
	client := &http.Client{Timeout: 15 * time.Second}

	for _, catName := range cats {
		fmt.Printf("Searching: %s... ", catName)
		cont := ""
		catAdded := 0

		for catAdded < 1250 {
			apiURL := fmt.Sprintf("https://commons.wikimedia.org/w/api.php?action=query&list=categorymembers&cmtitle=Category:%s&cmtype=file&cmlimit=100&format=json&cmcontinue=%s", catName, url.QueryEscape(cont))

			req, _ := http.NewRequest("GET", apiURL, nil)
			req.Header.Set("User-Agent", UserAgent)
			resp, err := client.Do(req)
			if err != nil {
				fmt.Printf("API Error: %v\n", err)
				break
			}

			var res struct {
				Continue struct{ CMContinue string }
				Query    struct{ CategoryMembers []struct{ Title string } }
			}
			if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
				fmt.Printf("JSON Error: %v\n", err)
				resp.Body.Close()
				break
			}
			resp.Body.Close()

			if len(res.Query.CategoryMembers) == 0 {
				fmt.Println("No members found.")
				break
			}

			for _, item := range res.Query.CategoryMembers {
				title := strings.TrimPrefix(item.Title, "File:")
				rawURL := fmt.Sprintf("https://commons.wikimedia.org/wiki/Special:FilePath/%s", url.PathEscape(title))

				manifest = append(manifest, MediaFile{
					URL:      rawURL,
					Title:    item.Title,
					Category: catName,
				})
				catAdded++
				if len(manifest) >= TotalImages {
					fmt.Printf("Found %d\n", catAdded)
					return manifest
				}
			}
			fmt.Printf("Found %d so far...\r", catAdded)

			if res.Continue.CMContinue == "" {
				break
			}
			cont = res.Continue.CMContinue
		}
		fmt.Printf("Finished category with %d images.\n", catAdded)
	}
	return manifest
}

func loadManifest() ([]MediaFile, error) {
	d, err := os.ReadFile(ManifestFile)
	if err != nil {
		return nil, err
	}
	var m []MediaFile
	err = json.Unmarshal(d, &m)
	return m, err
}

func saveManifest(m []MediaFile) {
	d, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(ManifestFile, d, 0644)
}
