package storage

import "os"

// Windows allocation and file identity are not measured in this slice.
func fileIdentity(info os.FileInfo) (string, int64, bool, bool) { return "", 0, false, false }
func openDirectory(path string) (*os.File, error)               { return os.Open(path) }
