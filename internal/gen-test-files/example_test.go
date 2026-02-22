package gentestfiles_test

import (
	"log"

	gentestfiles "github.com/lbe/sfpg-go/internal/gen-test-files"
)

func Example() {
	filePaths := []string{
		"file1.txt",
		"file2.html",
		"images/photo.jpg",
		"images/icon.png",
		"graphics/animation.gif",
		"nested/deep/path/document.txt",
	}

	if err := gentestfiles.CreateTestFiles("test_output", filePaths); err != nil {
		log.Fatal(err)
	}

	// Output files will be created in ./test_output/
}
