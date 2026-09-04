package persistence

// Go does not expose portable directory synchronization on Windows. The candidate
// file is synced before replacement; power-loss durability is not claimed.
func syncDirectory(string) error { return nil }
