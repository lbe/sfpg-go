package tableswap

// destName returns the destination table name used during a cache table rotation.
func destName(name string) string {
	return name + "_new"
}

// staleName returns the stale table name that will be dropped after a cache table rotation.
func staleName(name string) string {
	return name + "_to_be_dropped"
}
