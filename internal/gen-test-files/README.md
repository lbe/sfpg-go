# gen-test-files

A Go package for creating valid test files with appropriate content based on file extensions.

## Features

- Creates valid files for: `.txt`, `.html`, `.jpg`, `.jpeg`, `.gif`, `.png`
- Generates images with random gradient patterns (200x190 pixels)
- Automatically creates nested directory structures
- Logs errors and continues processing remaining files
- Overwrites duplicate file paths

## Usage

```go
import gentestfiles "your-module-path/internal/gen-test-files"

filePaths := []string{
    "file1.txt",
    "images/photo.jpg",
    "nested/path/document.html",
}

err := gentestfiles.CreateTestFiles("output_dir", filePaths)
if err != nil {
    log.Fatal(err)
}
```
