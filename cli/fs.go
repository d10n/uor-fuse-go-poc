package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	artifactspec "github.com/oras-project/artifacts-spec/specs-go/v1"
	"github.com/uor-framework/uor-client-go/attributes/matchers"
	"github.com/uor-framework/uor-client-go/model"
	"github.com/uor-framework/uor-client-go/nodes/descriptor"
	"github.com/uor-framework/uor-client-go/ocimanifest"
	"github.com/uor-framework/uor-client-go/registryclient"
	"github.com/uor-framework/uor-fuse-go/cli/log"
	"github.com/winfsp/cgofuse/fuse"
	"oras.land/oras-go/v2/content"
)

type DecayCache struct {
	data     *[]byte
	decay    *time.Timer
	duration *time.Duration
	refCount *int32
	logger   *log.Logger
}

func NewDecayCache(data *[]byte, duration *time.Duration, logger *log.Logger) *DecayCache {
	cache := &DecayCache{
		data:     data,
		duration: duration,
		logger:   logger,
		refCount: new(int32),
	}
	*cache.refCount = 0
	return cache
}

func (c *DecayCache) Flush() {
	if c.decay != nil {
		c.decay.Stop()
		c.decay = nil
	}
	c.data = nil
	(*c.logger).Debugf("Flushed cache for file")
}
func (c *DecayCache) AddUser() {
	if c.decay != nil {
		c.decay.Stop()
		c.decay = nil
		(*c.logger).Debugf("Removing cache decay timer")
	}
	atomic.AddInt32(c.refCount, 1)
}
func (c *DecayCache) RemoveUser() {
	atomic.AddInt32(c.refCount, -1)
	//c.refCount -= 1
	if *c.refCount == 0 {
		c.decay = time.AfterFunc(*c.duration, c.Flush)
		(*c.logger).Debugf("Setting cache decay timer")
	}
}

type UorFs struct {
	fuse.FileSystemBase
	*MountOptions
	client  registryclient.Remote
	matcher matchers.PartialAttributeMatcher

	euid uint32
	egid uint32

	mutex sync.Mutex
	ino   uint64
	root  *node_t
	ctx   context.Context

	cacheDuration *time.Duration
}

type node_t struct {
	stat     fuse.Stat_t
	xattrs   map[string][]byte
	children map[string]*node_t
	data     *DecayCache
	desc     *ocispec.Descriptor
}

func newNode(dev uint64, ino uint64, mode uint32, uid uint32, gid uint32) *node_t {
	tmsp := fuse.Now()
	node := node_t{
		fuse.Stat_t{
			Dev:      dev,
			Ino:      ino,
			Mode:     mode,
			Nlink:    1,
			Uid:      uid,
			Gid:      gid,
			Atim:     tmsp,
			Mtim:     tmsp,
			Ctim:     tmsp,
			Birthtim: tmsp,
			Flags:    0,
		},
		nil,
		nil,
		nil,
		nil,
	}
	if fuse.S_IFDIR == node.stat.Mode&fuse.S_IFMT {
		node.children = map[string]*node_t{}
		node.stat.Nlink = 2 // 1 from parent + 1 from self
	}
	return &node
}

func (fs *UorFs) lookupNode(path string) *node_t {
	pathParts := strings.Split(path, "/")
	parent := fs.root
	node := parent
	if path == "/" {
		return fs.root
	}
	for i, part := range pathParts {
		if part == "" {
			continue
		}
		if len(part) > 255 {
			panic(fuse.Error(-fuse.ENAMETOOLONG))
		}
		node = node.children[part]
		if node == nil {
			return nil
		}
		if i == len(pathParts)-1 {
			return node
		}
	}
	return nil
}

func (fs *UorFs) Open(path string, flags int) (errc int, fh uint64) {
	defer fs.synchronize()()
	if node := fs.lookupNode(path); node != nil {
		return 0, 0
	}
	return -fuse.ENOENT, ^uint64(0)
}

func (fs *UorFs) Getattr(path string, stat *fuse.Stat_t, fh uint64) (errc int) {
	defer fs.synchronize()()
	fs.Logger.Infof("Getattr path: %v", path)

	node := fs.lookupNode(path)
	if node == nil {
		return -fuse.ENOENT
	}

	stat.Mode = node.stat.Mode
	stat.Size = node.stat.Size

	stat.Dev = node.stat.Dev
	stat.Ino = node.stat.Ino
	stat.Nlink = node.stat.Nlink
	stat.Uid = node.stat.Uid
	stat.Gid = node.stat.Gid
	stat.Rdev = node.stat.Rdev
	stat.Atim = node.stat.Atim
	stat.Mtim = node.stat.Mtim
	stat.Ctim = node.stat.Ctim
	stat.Blksize = node.stat.Blksize
	stat.Blocks = node.stat.Blocks
	stat.Birthtim = node.stat.Birthtim
	stat.Flags = node.stat.Flags

	return 0
}

func (fs *UorFs) Read(path string, buff []byte, ofst int64, fh uint64) (n int) {
	defer fs.synchronize()()

	node := fs.lookupNode(path)
	if node == nil {
		return -fuse.ENOENT
	}

	if node.data == nil {
		nodeData, err := fs.client.GetContent(fs.ctx, fs.Source, *node.desc)
		if err != nil {
			return -fuse.ENOENT
		}

		node.data = NewDecayCache(&nodeData, fs.cacheDuration, &fs.Logger)
	}
	node.data.AddUser()
	defer node.data.RemoveUser()

	endofst := ofst + int64(len(buff))
	if endofst > int64(len(*node.data.data)) {
		endofst = int64(len(*node.data.data))
	}
	if endofst < ofst {
		return 0
	}
	n = copy(buff, (*node.data.data)[ofst:endofst])
	return
}

func (fs *UorFs) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64,
	fh uint64) (errc int) {
	defer fs.synchronize()()
	fill(".", nil, 0)
	fill("..", nil, 0)

	node := fs.lookupNode(path)
	if node == nil {
		return -fuse.ENOENT
	}

	for name, child := range node.children {
		if !fill(name, &child.stat, 0) {
			break
		}
	}
	return 0

}

func (fs *UorFs) Listxattr(path string, fill func(name string) bool) (errc int) {
	defer fs.synchronize()()
	node := fs.lookupNode(path)
	if node == nil {
		return -fuse.ENOENT
	}
	for name := range node.xattrs {
		if !fill(name) {
			return -fuse.ERANGE
		}
	}
	return 0
}

func (fs *UorFs) Getxattr(path string, name string) (errc int, xattr []byte) {
	defer fs.synchronize()()
	//node := fs.flats[path]
	node := fs.lookupNode(path)
	if node == nil {
		return -fuse.ENOENT, nil
	}
	if xattr, ok := node.xattrs[name]; !ok {
		return -fuse.ENOATTR, nil
	} else {
		return 0, xattr
	}

}

func (fs *UorFs) synchronize() func() {
	fs.mutex.Lock()
	return func() {
		fs.mutex.Unlock()
	}
}

// loadFromReference loads a collection from an image reference.
func (fs *UorFs) loadFromReference(ctx context.Context, reference string, client registryclient.Remote) error {

	layerDescriptors, err := getManifest(ctx, reference, client, fs.matcher)
	if err != nil {
		return err
	}

	for _, layerInfo := range layerDescriptors {
		layerInfo := layerInfo // fix &layerInfo

		switch layerInfo.MediaType {
		case ocimanifest.UORSchemaMediaType:
			continue
		case ocispec.MediaTypeImageConfig:
			continue
		case ocimanifest.UORConfigMediaType:
			continue
		case ocispec.MediaTypeImageManifest:
			continue
		}

		if layerInfo.Annotations == nil {
			fs.Logger.Debugf("layer unexpectedly had no annotations, ignoring: %v", layerInfo.Digest)
			continue
		}

		skip := func(_ string) bool { return false }
		attributeSet, err := ocimanifest.AnnotationsToAttributeSet(layerInfo.Annotations, skip)
		if err != nil {
			return err
		}
		//uorAttributes := layerInfo.Annotations[ocimanifest.AnnotationUORAttributes]
		filename, err := attributeSet.Find(ocispec.AnnotationTitle).AsString()
		if err != nil {
			return err
		}

		//uid, gid, _ := fuse.Getcontext()
		var node *node_t = newNode(0, 0, fuse.S_IFREG|00444, fs.euid, fs.egid) // 444

		node.desc = &layerInfo
		node.stat.Size = layerInfo.Size
		node.xattrs = map[string][]byte{}
		node.xattrs["user.uor.Digest"] = []byte(layerInfo.Digest.String())
		if layerInfo.MediaType != "" {
			node.xattrs["user.uor.MediaType"] = []byte(layerInfo.MediaType)
		}
		for _, attribute := range attributeSet.List() {
			if attribute.Key() == ocispec.AnnotationTitle {
				continue
			}
			if jsonObj, err := json.Marshal(attribute.AsAny()); err != nil {
				fs.Logger.Errorf("Unknown attribute value %v %v", attribute.Key(), err)
			} else {
				node.xattrs["user.uor.attributes."+attribute.Key()] = jsonObj
			}
		}
		fs.insertNode(filename, node)
	}

	return nil
}

func getManifest(ctx context.Context, reference string, client registryclient.Remote, matcher matchers.PartialAttributeMatcher) ([]ocispec.Descriptor, error) {
	manifestDesc, manifestRc, err := client.GetManifest(ctx, reference)
	if err != nil {
		return nil, err
	}
	manifestBytes, err := content.ReadAll(manifestRc, manifestDesc)
	if err != nil {
		return nil, err
	}
	//manifest, err := manifest.FromBlob(manifestBytes, manifestDesc.MediaType)
	manifest, err := successorBytesToManifest(ctx, manifestBytes, manifestDesc)
	fmt.Printf("%v\n", manifest)
	//return manifest, nil

	graph, err := client.LoadCollection(ctx, reference)
	if err != nil {
		return nil, err
	}

	// Filter the collection per the matcher criteria
	if matcher != nil {
		var matchedLeaf int
		matchFn := model.MatcherFunc(func(node model.Node) (bool, error) {
			// This check ensure we are not weeding out any manifests needed
			// for OCI DAG traversal.
			if len(graph.From(node.ID())) != 0 {
				return true, nil
			}

			// Check that this is a descriptor node and the blob is
			// not a config or schema resource.
			desc, ok := node.(*descriptor.Node)
			if !ok {
				return false, nil
			}

			switch desc.Descriptor().MediaType {
			case ocimanifest.UORSchemaMediaType:
				return true, nil
			case ocispec.MediaTypeImageConfig:
				return true, nil
			case ocimanifest.UORConfigMediaType:
				return true, nil
			}

			match, err := matcher.Matches(node)
			if err != nil {
				return false, err
			}

			if match {
				matchedLeaf++
			}

			return match, nil
		})

		var err error
		graph, err = graph.SubCollection(matchFn)
		if err != nil {
			return nil, err
		}

		if matchedLeaf == 0 {
			return []ocispec.Descriptor{}, nil
		}
	}

	result := []ocispec.Descriptor{}

	nodes := graph.Nodes()
	for _, node := range nodes {
		d, ok := node.(*descriptor.Node)
		if ok {
			result = append(result, d.Descriptor())
		}
	}

	return result, nil
}

// getSuccessor returns the nodes directly pointed by the current node. This is adapted from the `oras` content.Successors
// to allow the use of a function signature to pull descriptor content.
func successorBytesToManifest(ctx context.Context, content []byte, node ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	switch node.MediaType {
	case string(types.DockerManifestSchema2), ocispec.MediaTypeImageManifest:
		// docker manifest and oci manifest are equivalent for successors.
		var manifest ocispec.Manifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			return nil, err
		}
		return append([]ocispec.Descriptor{manifest.Config}, manifest.Layers...), nil
	case string(types.DockerManifestList), ocispec.MediaTypeImageIndex:
		// docker manifest list and oci index are equivalent for successors.
		var index ocispec.Index
		if err := json.Unmarshal(content, &index); err != nil {
			return nil, err
		}

		return index.Manifests, nil
	case artifactspec.MediaTypeArtifactManifest:
		var manifest artifactspec.Manifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			return nil, err
		}
		var nodes []ocispec.Descriptor
		if manifest.Subject != nil {
			nodes = append(nodes, artifactToOCI(*manifest.Subject))
		}
		for _, blob := range manifest.Blobs {
			nodes = append(nodes, artifactToOCI(blob))
		}
		return nodes, nil
	}
	return nil, nil
}

// artifactToOCI converts artifact descriptor to OCI descriptor.
func artifactToOCI(desc artifactspec.Descriptor) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType:   desc.MediaType,
		Digest:      desc.Digest,
		Size:        desc.Size,
		URLs:        desc.URLs,
		Annotations: desc.Annotations,
	}
}

// TODO run periodically to detect changes?
func (fs *UorFs) buildFsNodes(ctx context.Context) {
	client := fs.client
	err := fs.loadFromReference(ctx, fs.Source, client)
	if err != nil {
		fs.Logger.Infof("%v", err)
		//return err
	}
}

func (fs *UorFs) insertNode(path string, node *node_t) {
	pathParts := strings.Split(path, "/")
	parent := fs.root
	for i, part := range pathParts {
		if parent.children == nil {
			parent.children = map[string]*node_t{}
		}
		if i == len(pathParts)-1 {
			parent.children[part] = node
		} else {
			if parent.children[part] == nil {
				parent.stat.Nlink += 1
				parent.children[part] = newNode(0, 0, fuse.S_IFDIR|00555, fs.euid, fs.egid)
			}
			parent = parent.children[part]
		}
	}
}

func NewUorFs(ctx context.Context, o MountOptions, client registryclient.Client, matcher matchers.PartialAttributeMatcher) *UorFs {
	duration := 5 * time.Minute
	fs := UorFs{
		MountOptions:  &o,
		client:        client,
		matcher:       matcher,
		ctx:           ctx,
		cacheDuration: &duration,
	}
	defer fs.synchronize()()
	//fs.ino++
	//uid, gid, _ := fuse.Getcontext()
	fs.euid, fs.egid = uint32(os.Geteuid()), uint32(os.Getegid())
	fs.root = newNode(0, 1, fuse.S_IFDIR|00555, fs.euid, fs.egid)
	//fs.flats = map[string]*node_t{}

	fs.buildFsNodes(ctx)
	return &fs
}
