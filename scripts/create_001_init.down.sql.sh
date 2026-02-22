#/usr/bin/env bash

DB_FILE="${1:-database.db}"
OUTPUT_FILE="${2:-drop_all.sql}"

if [ ! -f "$DB_FILE" ]; then
  echo "Error: Database file '$DB_FILE' not found"
  exit 1
fi

# Generate SQL script
{
  echo "-- Disable foreign key constraints"
  echo "PRAGMA foreign_keys = OFF;"
  echo ""

  echo "-- Drop all views"
  sqlite3 "$DB_FILE" \
    "SELECT 'DROP VIEW IF EXISTS ' || name || ';' FROM sqlite_master WHERE type='view';"
  echo ""

  echo "-- Drop all tables"
  sqlite3 "$DB_FILE" \
    "SELECT 'DROP TABLE IF EXISTS ' || name || ';' FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%';"

} > "$OUTPUT_FILE"

echo "SQL script generated: $OUTPUT_FILE"
echo "To execute: sqlite3 $DB_FILE < $OUTPUT_FILE"
