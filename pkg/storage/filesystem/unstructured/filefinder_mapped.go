package unstructured

import (
	"context"
	"errors"
	"sync"

	"github.com/weaveworks/libgitops/pkg/storage/core"
	"github.com/weaveworks/libgitops/pkg/storage/filesystem"
	utilerrs "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
)

var (
	// ErrNotTracked is returned when the requested resource wasn't found.
	ErrNotTracked = errors.New("untracked object")
)

// GenericFileFinder implements FileFinder.
var _ FileFinder = &GenericFileFinder{}

// NewGenericFileFinder creates a new instance of GenericFileFinder,
// that implements the FileFinder interface. The contentTyper is optional,
// by default core.DefaultContentTyper will be used.
func NewGenericFileFinder(contentTyper filesystem.ContentTyper, fs filesystem.Filesystem) FileFinder {
	if contentTyper == nil {
		contentTyper = filesystem.DefaultContentTyper
	}
	if fs == nil {
		panic("NewGenericFileFinder: fs is mandatory")
	}
	return &GenericFileFinder{
		contentTyper: contentTyper,
		fs:           fs,
		// TODO: Support multiple branches
		branch:    &branchImpl{},
		pathToIDs: make(map[string]core.UnversionedObjectIDSet),
		mu:        &sync.RWMutex{},
	}
}

// GenericFileFinder is a generic implementation of FileFinder.
// It uses a ContentTyper to identify what content type a file uses.
//
// This implementation relies on that all information about what files exist
// is fed through SetMapping(s). If a file or ID is requested that doesn't
// exist in the internal cache, ErrNotTracked will be returned.
//
// Hence, this implementation does not at the moment support creating net-new
// Objects without someone calling SetMapping() first.
type GenericFileFinder struct {
	// Default: DefaultContentTyper
	contentTyper filesystem.ContentTyper
	fs           filesystem.Filesystem

	branch    branch
	pathToIDs map[string]core.UnversionedObjectIDSet
	// mu guards branch and pathToIDs
	mu *sync.RWMutex
}

func (f *GenericFileFinder) Filesystem() filesystem.Filesystem {
	return f.fs
}

func (f *GenericFileFinder) ContentTyper() filesystem.ContentTyper {
	return f.contentTyper
}

// ObjectPath gets the file path relative to the root directory
func (f *GenericFileFinder) ObjectPath(ctx context.Context, id core.UnversionedObjectID) (string, error) {
	cp, ok := f.GetMapping(ctx, id)
	if !ok {
		// TODO: separate interface for "new creates"?
		return "", utilerrs.NewAggregate([]error{ErrNotTracked, core.NewErrNotFound(id)})
	}
	return cp.Path, nil
}

// ObjectsAt retrieves the ObjectIDs in the file with the given relative file path.
func (f *GenericFileFinder) ObjectsAt(ctx context.Context, path string) (core.UnversionedObjectIDSet, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	// TODO: This needs to be per-branch too
	ids, ok := f.pathToIDs[path]
	if !ok {
		// TODO: Support "creation" of Objects easier, in a generic way through an interface, e.g.
		// NewObjectPlacer?
		return nil, ErrNotTracked
	}
	return ids, nil
}

// ListNamespaces lists the available namespaces for the given GroupKind.
// This function shall only be called for namespaced objects, it is up to
// the caller to make sure they do not call this method for root-spaced
// objects. If any of the given rules are violated, ErrNamespacedMismatch
// should be returned as a wrapped error.
//
// The implementer can choose between basing the answer strictly on e.g.
// v1.Namespace objects that exist in the system, or just the set of
// different namespaces that have been set on any object belonging to
// the given GroupKind.
func (f *GenericFileFinder) ListNamespaces(ctx context.Context, gk core.GroupKind) (sets.String, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	m := f.branch.groupKind(gk).raw()
	nsSet := sets.NewString()
	for ns := range m {
		nsSet.Insert(ns)
	}
	return nsSet, nil
}

// ListObjectIDs returns a list of unversioned ObjectIDs.
// For namespaced GroupKinds, the caller must provide a namespace, and for
// root-spaced GroupKinds, the caller must not. When namespaced, this function
// must only return object IDs for that given namespace. If any of the given
// rules are violated, ErrNamespacedMismatch should be returned as a wrapped error.
func (f *GenericFileFinder) ListObjectIDs(ctx context.Context, gk core.GroupKind, namespace string) (core.UnversionedObjectIDSet, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	m := f.branch.groupKind(gk).namespace(namespace).raw()
	ids := make([]core.UnversionedObjectID, 0, len(m))
	for name := range m {
		ids = append(ids, core.NewUnversionedObjectID(gk, core.ObjectKey{Name: name, Namespace: namespace}))
	}
	return core.NewUnversionedObjectIDSet(ids...), nil
}

// GetMapping retrieves a mapping in the system
func (f *GenericFileFinder) GetMapping(ctx context.Context, id core.UnversionedObjectID) (ChecksumPath, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.getMapping(ctx, id)
}

// getMapping is like GetMapping; but without a read lock; for internal operations
func (f *GenericFileFinder) getMapping(ctx context.Context, id core.UnversionedObjectID) (ChecksumPath, bool) {
	cp, ok := f.branch.
		groupKind(id.GroupKind()).
		namespace(id.ObjectKey().Namespace).
		name(id.ObjectKey().Name)
	return cp, ok
}

// SetMapping binds an ID's virtual path to a physical file path
func (f *GenericFileFinder) SetMapping(ctx context.Context, id core.UnversionedObjectID, checksumPath ChecksumPath) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.branch.
		groupKind(id.GroupKind()).
		namespace(id.ObjectKey().Namespace).
		setName(id.ObjectKey().Name, checksumPath)

	// Create the mapping between the path and a set of IDs if it doesn't exist
	_, ok := f.pathToIDs[checksumPath.Path]
	if !ok {
		f.pathToIDs[checksumPath.Path] = core.NewUnversionedObjectIDSet()
	}
	// Register the ID with the given path
	f.pathToIDs[checksumPath.Path].Insert(id)
}

// ResetMappings replaces all mappings at once
func (f *GenericFileFinder) ResetMappings(ctx context.Context, m map[core.UnversionedObjectID]ChecksumPath) {
	f.branch = &branchImpl{}
	for id, cp := range m {
		f.SetMapping(ctx, id, cp)
	}
}

// DeleteMapping removes the physical file path mapping
// matching the given id
func (f *GenericFileFinder) DeleteMapping(ctx context.Context, id core.UnversionedObjectID) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cp, ok := f.getMapping(ctx, id)
	if !ok {
		// Nothing to delete if it doesn't exist yet
		return
	}
	// Delete it from the cache
	f.branch.
		groupKind(id.GroupKind()).
		namespace(id.ObjectKey().Namespace).
		deleteName(id.ObjectKey().Name)
	// Delete the related ID from the path mapping too
	f.pathToIDs[cp.Path].Delete(id)
	// If the length of the set was shrunk to zero; delete it from the map completely
	if f.pathToIDs[cp.Path].Len() == 0 {
		delete(f.pathToIDs, cp.Path)
	}
}
