package cache

import (
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"
)

var bucketImages = []byte("images")

type Bolt struct {
	db *bolt.DB
}

type boltEntry struct {
	Value     string    `json:"value"`
	ExpiresAt time.Time `json:"expires_at"`
}

func OpenBolt(path string) (*Bolt, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketImages)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Bolt{db: db}, nil
}

func (b *Bolt) Get(key string) (string, bool) {
	var item boltEntry
	err := b.db.View(func(tx *bolt.Tx) error {
		value := tx.Bucket(bucketImages).Get([]byte(key))
		if value == nil {
			return nil
		}
		return json.Unmarshal(value, &item)
	})
	if err != nil || item.Value == "" || time.Now().After(item.ExpiresAt) {
		if item.Value != "" {
			_ = b.db.Update(func(tx *bolt.Tx) error {
				return tx.Bucket(bucketImages).Delete([]byte(key))
			})
		}
		return "", false
	}
	return item.Value, true
}

func (b *Bolt) Set(key, value string, ttl time.Duration) error {
	item := boltEntry{Value: value, ExpiresAt: time.Now().Add(ttl)}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return b.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketImages).Put([]byte(key), data)
	})
}

func (b *Bolt) Close() error {
	return b.db.Close()
}
