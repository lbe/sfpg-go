package server

// REMOVED: TestPathHandling - Slow duplicate test (1.38s)
// REMOVED: func TestPathHandling(t *testing.T) {
// REMOVED: 	// Suppress slog output during test
// REMOVED: 	discardHandler := slog.NewTextHandler(io.Discard, nil)
// REMOVED: 	slog.SetDefault(slog.New(discardHandler))
// REMOVED:
// REMOVED: 	app := CreateApp(t, true)
// REMOVED: 	defer app.Shutdown()
// REMOVED:
// REMOVED: 	// Create a dummy JPEG image file in the images directory
// REMOVED: 	// This path will be absolute, simulating what parallelwalkdir returns.
// REMOVED: 	absoluteImagePath := filepath.Join(app.imagesDir, "test_gallery", "test_image.jpg")
// REMOVED: 	err := os.MkdirAll(filepath.Dir(absoluteImagePath), 0o755)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create dummy image directory: %v", err)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Create a simple 1x1 red JPEG image
// REMOVED: 	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
// REMOVED: 	file, err := os.Create(absoluteImagePath)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create dummy image file: %v", err)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	err = jpeg.Encode(file, img, nil)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to encode dummy JPEG image: %v", err)
// REMOVED: 	}
// REMOVED: 	err = file.Close()
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to close dummy image file: %v", err)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Enqueue the absolute path, simulating walkImageDir's behavior
// REMOVED: 	err = app.q.Enqueue(absoluteImagePath)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to enqueue absolute path: %v", err)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Wait for the worker pool to process the item
// REMOVED: 	// Give it enough time to ensure processing completes
// REMOVED: 	time.Sleep(1 * time.Second)
// REMOVED:
// REMOVED: 	// Expected relative path in the database
// REMOVED: 	expectedRelativePath := "test_gallery/test_image.jpg"
// REMOVED:
// REMOVED: 	// Verify file path in database is relative
// REMOVED: 	cpcRo, err := app.dbRoPool.Get()
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get RO DB connection: %v", err)
// REMOVED: 	}
// REMOVED: 	defer app.dbRoPool.Put(cpcRo)
// REMOVED:
// REMOVED: 	fileView, err := cpcRo.Queries.GetFileViewByPath(app.ctx, expectedRelativePath)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get file view by path %q: %v", expectedRelativePath, err)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	if fileView.Path != expectedRelativePath {
// REMOVED: 		t.Errorf("expected file path in DB to be %q, got %q", expectedRelativePath, fileView.Path)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify thumbnail was generated (by checking for its existence in DB)
// REMOVED: 	exists, err := cpcRo.Queries.GetThumbnailExistsViewByID(app.ctx, fileView.ID)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to check thumbnail existence for file ID %d: %v", fileView.ID, err)
// REMOVED: 	}
// REMOVED: 	if !exists {
// REMOVED: 		t.Error("thumbnail was not generated or not recorded in DB")
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify folder path in database is relative
// REMOVED: 	expectedRelativeFolderPath := "test_gallery"
// REMOVED: 	folderView, err := cpcRo.Queries.GetFolderViewByPath(app.ctx, expectedRelativeFolderPath)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get folder view by path %q: %v", expectedRelativeFolderPath, err)
// REMOVED: 	}
// REMOVED: 	if folderView.Path != expectedRelativeFolderPath {
// REMOVED: 		t.Errorf("expected folder path in DB to be %q, got %q", expectedRelativeFolderPath, folderView.Path)
// REMOVED: 	}
// REMOVED: }
