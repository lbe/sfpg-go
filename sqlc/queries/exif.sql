-- name: UpsertExif :exec
INSERT INTO exif_metadata (file_id, camera_make, camera_model, lens_model,
                           focal_length, aperture, shutter_speed, iso,
                           orientation, latitude, longitude, altitude, capture_date)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(file_id) DO UPDATE SET
  camera_make=excluded.camera_make,
  camera_model=excluded.camera_model,
  lens_model=excluded.lens_model,
  focal_length=excluded.focal_length,
  aperture=excluded.aperture,
  shutter_speed=excluded.shutter_speed,
  iso=excluded.iso,
  orientation=excluded.orientation,
  latitude=excluded.latitude,
  longitude=excluded.longitude,
  altitude=excluded.altitude,
  capture_date=excluded.capture_date;

-- name: GetExifByFile :one
SELECT * FROM exif_metadata WHERE file_id = ?;

