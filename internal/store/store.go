// Package store declares the persistence errors shared by storage
// implementations and their consumers.
//
// It exists so transport layers can recognise "this id does not exist"
// without importing a concrete database driver package.
package store

import "errors"

// ErrNotFound reports that a requested row does not exist.
//
// Implementations wrap it (or return it directly) so callers can use
// errors.Is without knowing which store produced the failure.
var ErrNotFound = errors.New("store: not found")
