// Package store 用 bbolt 保存任务与自定义转码预设。
// 数据库文件位于可执行文件所在目录（可用 BATCHER_DATA_DIR 覆盖），随程序一起搬走。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gsxhnd/batcher/internal/presets"
	"github.com/gsxhnd/batcher/internal/task"
	bolt "go.etcd.io/bbolt"
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
	bolt *bolt.DB
	path string
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
		return nil, fmt.Errorf("数据目录不能为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s: %w", dir, err)
	}
	path := filepath.Join(dir, FileName)
	b, err := bolt.Open(path, 0o644, &bolt.Options{Timeout: time.Second})
	if err != nil {
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
		return fmt.Errorf("任务 ID 不能为空")
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
	var data []byte
	err := db.bolt.View(func(tx *bolt.Tx) error {
		data = tx.Bucket(bucketTasks).Get([]byte(id))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("读取任务 %s: %w", id, err)
	}
	if data == nil {
		return nil, fmt.Errorf("未找到任务 %s", id)
	}
	return decodeTask(id, data)
}

// ListTasks 返回全部主任务，按 ID 字典序（时间前缀，从旧到新）。
func (db *DB) ListTasks() ([]*task.Batch, error) {
	var out []*task.Batch
	err := db.bolt.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTasks).ForEach(func(k, v []byte) error {
			b, err := decodeTask(string(k), v)
			if err != nil {
				return err
			}
			out = append(out, b)
			return nil
		})
	})
	if err != nil {
		return nil, err
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
	var data []byte
	err := db.bolt.View(func(tx *bolt.Tx) error {
		data = tx.Bucket(bucketPresets).Get(keyPresetList)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("读取预设: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var list []presets.Preset
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("解析预设: %w", err)
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

// importLegacyPresets 在第一次打开数据库时，把旧的 advance_presets.json 导入预设桶。
func (db *DB) importLegacyPresets() error {
	var imported bool
	err := db.bolt.View(func(tx *bolt.Tx) error {
		imported = len(tx.Bucket(bucketMeta).Get(keyImported)) > 0
		return nil
	})
	if err != nil {
		return fmt.Errorf("读取预设迁移标记: %w", err)
	}
	if imported {
		return nil
	}
	existing, err := db.LoadPresets()
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		if dir, err := presets.Dir(); err == nil {
			legacy, err := presets.LoadFrom(dir)
			if err != nil {
				return fmt.Errorf("导入旧预设: %w", err)
			}
			if len(legacy) > 0 {
				if err := db.SavePresets(legacy); err != nil {
					return err
				}
			}
		}
	}
	err = db.bolt.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put(keyImported, []byte("1"))
	})
	if err != nil {
		return fmt.Errorf("写入预设迁移标记: %w", err)
	}
	return nil
}
