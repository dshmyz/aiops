package capabilities

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileCapabilityStore 是 CapabilityStore 的文件系统实现：能力以 yaml 文件形式存储在
// discovered/ 与 published/ 目录下。第一阶段与原 Manager 行为完全一致，为后续
// SQLCapabilityStore（多节点一致）铺路。
type FileCapabilityStore struct {
	root string
}

func NewFileCapabilityStore(root string) *FileCapabilityStore {
	return &FileCapabilityStore{root: strings.TrimSpace(root)}
}

func (s *FileCapabilityStore) Configured() error {
	if strings.TrimSpace(s.root) == "" {
		return ErrCapabilityRootNotConfigured
	}
	return nil
}

func (s *FileCapabilityStore) ListAll(_ context.Context) ([]ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return nil, err
	}
	items := []ManagedCapability{}
	for _, source := range []string{SourceDiscovered, SourcePublished} {
		dir := filepath.Join(s.root, source)
		paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
		if err != nil {
			return nil, err
		}
		sort.Strings(paths)
		for _, path := range paths {
			item, err := s.readPath(path, source)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].Source != items[right].Source {
			return sourceRank(items[left].Source) < sourceRank(items[right].Source)
		}
		return items[left].Name < items[right].Name
	})
	return items, nil
}

func (s *FileCapabilityStore) Get(_ context.Context, name string) (ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return ManagedCapability{}, err
	}
	for _, source := range []string{SourceDiscovered, SourcePublished} {
		path, err := s.pathFor(source, name)
		if err != nil {
			return ManagedCapability{}, err
		}
		if _, err := os.Stat(path); err == nil {
			return s.readPath(path, source)
		} else if !os.IsNotExist(err) {
			return ManagedCapability{}, err
		}
	}
	return ManagedCapability{}, ErrCapabilityNotFound
}

func (s *FileCapabilityStore) SaveDraft(_ context.Context, capability Capability) (ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return ManagedCapability{}, err
	}
	if err := validateManagedCapabilityName(capability.Name); err != nil {
		return ManagedCapability{}, err
	}
	path, err := s.pathFor(SourceDiscovered, capability.Name)
	if err != nil {
		return ManagedCapability{}, err
	}
	if err := writeCapabilityFile(path, capability); err != nil {
		return ManagedCapability{}, err
	}
	return s.readPath(path, SourceDiscovered)
}

func (s *FileCapabilityStore) SavePublished(_ context.Context, capability Capability) (ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return ManagedCapability{}, err
	}
	if err := validateManagedCapabilityName(capability.Name); err != nil {
		return ManagedCapability{}, err
	}
	path, err := s.pathFor(SourcePublished, capability.Name)
	if err != nil {
		return ManagedCapability{}, err
	}
	if err := writeCapabilityFile(path, capability); err != nil {
		return ManagedCapability{}, err
	}
	return s.readPath(path, SourcePublished)
}

func (s *FileCapabilityStore) Has(_ context.Context, source string, name string) (bool, error) {
	if err := s.Configured(); err != nil {
		return false, err
	}
	path, err := s.pathFor(source, name)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *FileCapabilityStore) MoveDraftToPublished(_ context.Context, name string) (ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return ManagedCapability{}, err
	}
	if err := validateManagedCapabilityName(name); err != nil {
		return ManagedCapability{}, err
	}
	srcPath, err := s.pathFor(SourceDiscovered, name)
	if err != nil {
		return ManagedCapability{}, err
	}
	item, err := s.readPath(srcPath, SourceDiscovered)
	if err != nil {
		if os.IsNotExist(err) {
			return ManagedCapability{}, ErrCapabilityNotFound
		}
		return ManagedCapability{}, err
	}
	capability := item.Capability
	capability.Status = StatusPublished
	dstPath, err := s.pathFor(SourcePublished, capability.Name)
	if err != nil {
		return ManagedCapability{}, err
	}
	if _, err := os.Stat(dstPath); err == nil {
		return ManagedCapability{}, fmt.Errorf("%w: %q is already published, unpublish the old version first", ErrCapabilityNameConflict, capability.Name)
	} else if !os.IsNotExist(err) {
		return ManagedCapability{}, err
	}
	if err := writeCapabilityFile(dstPath, capability); err != nil {
		return ManagedCapability{}, err
	}
	if err := os.Remove(srcPath); err != nil {
		return ManagedCapability{}, err
	}
	return s.readPath(dstPath, SourcePublished)
}

func (s *FileCapabilityStore) MovePublishedToDraft(_ context.Context, name string) (ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return ManagedCapability{}, err
	}
	if err := validateManagedCapabilityName(name); err != nil {
		return ManagedCapability{}, err
	}
	srcPath, err := s.pathFor(SourcePublished, name)
	if err != nil {
		return ManagedCapability{}, err
	}
	item, err := s.readPath(srcPath, SourcePublished)
	if err != nil {
		if os.IsNotExist(err) {
			return ManagedCapability{}, ErrCapabilityNotFound
		}
		return ManagedCapability{}, err
	}
	capability := item.Capability
	capability.Status = StatusNeedsReview
	dstPath, err := s.pathFor(SourceDiscovered, capability.Name)
	if err != nil {
		return ManagedCapability{}, err
	}
	if _, err := os.Stat(dstPath); err == nil {
		return ManagedCapability{}, fmt.Errorf("%w: %q already exists as a draft, remove the draft first", ErrCapabilityNameConflict, capability.Name)
	} else if !os.IsNotExist(err) {
		return ManagedCapability{}, err
	}
	if err := writeCapabilityFile(dstPath, capability); err != nil {
		return ManagedCapability{}, err
	}
	if err := os.Remove(srcPath); err != nil {
		return ManagedCapability{}, err
	}
	return s.readPath(dstPath, SourceDiscovered)
}

// pathFor 构造能力文件路径并校验 name/source。
func (s *FileCapabilityStore) pathFor(source, name string) (string, error) {
	if err := validateManagedCapabilityName(name); err != nil {
		return "", err
	}
	if source != SourceDiscovered && source != SourcePublished {
		return "", fmt.Errorf("unknown capability source %q", source)
	}
	return filepath.Join(s.root, source, name+".yaml"), nil
}

// readPath 读取一个 yaml 文件并返回 ManagedCapability（含校验结果与修改时间）。
func (s *FileCapabilityStore) readPath(path, source string) (ManagedCapability, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return ManagedCapability{}, err
	}
	var capability Capability
	if err := yaml.Unmarshal(body, &capability); err != nil {
		return ManagedCapability{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return ManagedCapability{}, err
	}
	return ManagedCapability{
		Capability: capability,
		Source:     source,
		Path:       path,
		ModifiedAt: info.ModTime(),
		Validation: validationFromError(Validate(capability)),
	}, nil
}

// validationFromError 把 Validate 返回的 error 转为 ValidationResult。
func validationFromError(err error) ValidationResult {
	if err == nil {
		return ValidationResult{Valid: true}
	}
	return ValidationResult{Valid: false, Error: err.Error()}
}

// writeCapabilityFile 安全写入 yaml 文件（先写临时文件再 rename，避免写入中断导致损坏）。
func writeCapabilityFile(path string, capability Capability) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := yaml.Marshal(capability)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempName)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	return nil
}

// validateManagedCapabilityName 校验能力名是否合法（非空、无路径分隔符、无遍历）。
func validateManagedCapabilityName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return ErrInvalidCapabilityName
	}
	return nil
}

// sourceRank 给 discovered/published 排序用的优先级（discovered 排前面，先展示草稿）。
func sourceRank(source string) int {
	if source == SourceDiscovered {
		return 0
	}
	return 1
}

// readPathWithTime 是兼容旧 Manager 代码的保留方法，内部委托 readPath。
func readPathWithTime(path, source string) (ManagedCapability, error) {
	store := FileCapabilityStore{root: filepath.Dir(filepath.Dir(path))} // 向上两级
	return store.readPath(path, source)
}

// Ensure FileCapabilityStore satisfies the interface at compile time.
var _ CapabilityStore = (*FileCapabilityStore)(nil)
