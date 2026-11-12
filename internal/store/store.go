// Package store 用 bbolt 保存任务与自定义转码预设。
// 数据库文件位于可执行文件所在目录（可用 BATCHER_DATA_DIR 覆盖），随程序一起搬走。
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/gsxhnd/batcher/internal/task"
	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
)

const (
	// FileName 是数据库文件名，放在数据目录下。
	FileName = "batcher.db"
	// envDir 覆盖数据目录，测试和特殊部署用。
	envDir = "BATCHER_DATA_DIR"
)

var (
	bucketTasks   = []byte("tasks")
	bucketPresets = []byte("presets")
	bucketMeta    = []byte("meta")
	keyPresetList = []byte("list")
	keyImported   = []byte("presetsImported")
)

// executablePath 返回当前进程可执行文件路径，测试中可替换。
var executablePath = os.Executable

// DB 是打开的 bbolt 数据库。同一文件同时只应打开一次。
type DB struct {
	bolt     *bolt.DB
	path     string
	mu       sync.Mutex
	warnings []string
}

// Warnings 返回打开和读取过程中跳过的问题，例如损坏的任务记录或旧预设文件。
func (db *DB) Warnings() []string {
	if db == nil {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	return slices.Clone(db.warnings)
}

func (db *DB) addWarning(msg string) {
	db.mu.Lock()
	db.warnings = append(db.warnings, msg)
	db.mu.Unlock()
}

// Dir 返回数据目录：环境变量 BATCHER_DATA_DIR，否则是可执行文件所在目录。
func Dir() (string, error) {
	if v := os.Getenv(envDir); v != "" {
		return v, nil
	}
	exe, err := executablePath()
	if err != nil {
		return "", fmt.Errorf("解析可执行文件路径: %w", err)
	}
	if eval, err := filepath.EvalSymlinks(exe); err == nil {
		exe = eval
	}
	return filepath.Dir(exe), nil
}

// Open 打开默认位置的数据库，并在首次启动时导入旧的预设 JSON。
func Open() (*DB, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	db, err := OpenDir(dir)
	if err != nil {
		return nil, err
	}
	if err := db.importLegacyPresets(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenDir 在指定目录创建或打开 batcher.db。
func OpenDir(dir string) (*DB, error) {
	if dir == "" {
		return nil, errors.New("数据目录不能为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		if os.IsPermission(err) {
			return nil, fmt.Errorf("目录不可写，可设置 BATCHER_DATA_DIR: %s: %w", dir, err)
		}
		return nil, fmt.Errorf("创建数据目录 %s: %w", dir, err)
	}
	path := filepath.Join(dir, FileName)
	b, err := bolt.Open(path, 0o644, &bolt.Options{Timeout: time.Second})
	if err != nil {
		if errors.Is(err, berrors.ErrTimeout) {
			return nil, fmt.Errorf("另一个 batcher 实例正在使用数据库 %s", path)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("目录不可写，可设置 BATCHER_DATA_DIR: %s: %w", path, err)
		}
		return nil, fmt.Errorf("打开数据库 %s: %w", path, err)
	}
	err = b.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketTasks, bucketPresets, bucketMeta} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = b.Close()
		return nil, fmt.Errorf("初始化数据库: %w", err)
	}
	return &DB{bolt: b, path: path}, nil
}

// Path 返回数据库文件路径。
func (db *DB) Path() string { return db.path }

// Close 关闭数据库。
func (db *DB) Close() error {
	if db == nil || db.bolt == nil {
		return nil
	}
	if err := db.bolt.Close(); err != nil {
		return fmt.Errorf("关闭数据库: %w", err)
	}
	return nil
}

// SaveTask 写入或覆盖一条主任务。
func (db *DB) SaveTask(b *task.Batch) error {
	if b == nil || b.ID == "" {
		return errors.New("任务 ID 不能为空")
	}
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("编码任务: %w", err)
	}
	err = db.bolt.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTasks).Put([]byte(b.ID), data)
	})
	if err != nil {
		return fmt.Errorf("写入任务 %s: %w", b.ID, err)
	}
	return nil
}

// LoadTask 按 ID 读取主任务。
func (db *DB) LoadTask(id string) (*task.Batch, error) {
	var b *task.Batch
	err := db.bolt.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketTasks).Get([]byte(id))
		if data == nil {
			return nil
		}
		decoded, err := decodeTask(id, data)
		if err != nil {
			return err
		}
		b = decoded
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("读取任务 %s: %w", id, err)
	}
	if b == nil {
		return nil, fmt.Errorf("未找到任务 %s", id)
	}
	return b, nil
}

// ListTasks 返回全部主任务，按 ID 字典序（时间前缀，从旧到新）。
func (db *DB) ListTasks() ([]*task.Batch, error) {
	var out []*task.Batch
	var warns []string
	err := db.bolt.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTasks).ForEach(func(k, v []byte) error {
			b, err := decodeTask(string(k), v)
			if err != nil {
				warns = append(warns, fmt.Sprintf("跳过无法读取的任务 %s: %v", k, err))
				return nil
			}
			out = append(out, b)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	for _, msg := range warns {
		db.addWarning(msg)
	}
	return out, nil
}

// DeleteTask 删除一条主任务。不存在时不报错。
func (db *DB) DeleteTask(id string) error {
	err := db.bolt.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTasks).Delete([]byte(id))
	})
	if err != nil {
		return fmt.Errorf("删除任务 %s: %w", id, err)
	}
	return nil
}

func decodeTask(id string, data []byte) (*task.Batch, error) {
	var b task.Batch
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("解析任务 %s: %w", id, err)
	}
	if !task.SupportedRecord(b.Version) {
		return nil, fmt.Errorf("不支持的任务记录版本 %d: %s", b.Version, id)
	}
	if b.ID == "" {
		b.ID = id
	}
	return &b, nil
}

// LoadPresets 读取自定义转码预设。还没有保存过时返回空列表。
func (db *DB) LoadPresets() ([]presets.Preset, error) {
	var list []presets.Preset
	err := db.bolt.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketPresets).Get(keyPresetList)
		if len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("读取预设: %w", err)
	}
	return list, nil
}

// SavePresets 覆盖保存自定义转码预设，保留调用方给出的顺序。
func (db *DB) SavePresets(list []presets.Preset) error {
	if list == nil {
		list = []presets.Preset{}
	}
	data, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("编码预设: %w", err)
	}
	err = db.bolt.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketPresets).Put(keyPresetList, data)
	})
	if err != nil {
		return fmt.Errorf("写入预设: %w", err)
	}
	return nil
}

// importLegacyPresets 在第一次打开数据库时准备预设。
// 预设键已经存在就保持原样。键不存在时，有旧的 JSON 就导入，否则写入内置预设。
func (db *DB) importLegacyPresets() error {
	var hasList, imported bool
	err := db.bolt.View(func(tx *bolt.Tx) error {
		hasList = len(tx.Bucket(bucketPresets).Get(keyPresetList)) > 0
		imported = len(tx.Bucket(bucketMeta).Get(keyImported)) > 0
		return nil
	})
	if err != nil {
		return fmt.Errorf("读取预设迁移标记: %w", err)
	}
	if hasList {
		if imported {
			return nil
		}
		return db.markPresetsImported()
	}
	var list []presets.Preset
	if !imported {
		loaded, err := legacyPresetList()
		if err != nil {
			db.addWarning(fmt.Sprintf("旧预设文件无法导入，已改用内置预设: %v", err))
		} else {
			list = loaded
		}
	}
	if len(list) == 0 {
		for _, b := range presets.Builtins() {
			list = append(list, presets.Preset(b))
		}
	}
	data, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("编码预设: %w", err)
	}
	err = db.bolt.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucketPresets).Put(keyPresetList, data); err != nil {
			return err
		}
		return tx.Bucket(bucketMeta).Put(keyImported, []byte("1"))
	})
	if err != nil {
		return fmt.Errorf("写入预设: %w", err)
	}
	return nil
}

func legacyPresetList() ([]presets.Preset, error) {
	dir, err := presets.Dir()
	if err != nil {
		return nil, nil
	}
	list, err := presets.LoadFrom(dir)
	if err != nil {
		return nil, fmt.Errorf("导入旧预设: %w", err)
	}
	return list, nil
}

func (db *DB) markPresetsImported() error {
	err := db.bolt.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put(keyImported, []byte("1"))
	})
	if err != nil {
		return fmt.Errorf("写入预设迁移标记: %w", err)
	}
	return nil
}
