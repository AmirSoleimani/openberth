package datastore

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/xid"
	_ "modernc.org/sqlite"
)

const (
	maxDocSize          = 100 * 1024 // 100 KB
	maxDocsPerCollection = 10_000
	maxCollections       = 100
	maxDBSize            = 50 * 1024 * 1024 // 50 MB
)

type Document struct {
	ID         string          `json:"id"`
	Collection string          `json:"collection"`
	Data       json.RawMessage `json:"data"`
	CreatedAt  string          `json:"createdAt"`
	UpdatedAt  string          `json:"updatedAt"`
}

type CollectionInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type Manager struct {
	persistDir string
	mu         sync.Mutex
	dbs        map[string]*sql.DB
}

func NewManager(persistDir string) *Manager {
	return &Manager{
		persistDir: persistDir,
		dbs:        make(map[string]*sql.DB),
	}
}

func (m *Manager) getDB(deploymentID string) (*sql.DB, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if db, ok := m.dbs[deploymentID]; ok {
		return db, nil
	}

	dir := filepath.Join(m.persistDir, deploymentID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create persist dir: %w", err)
	}

	dbPath := filepath.Join(dir, "store.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS documents (
			id TEXT PRIMARY KEY,
			collection TEXT NOT NULL,
			data JSON NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_collection ON documents(collection, created_at);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	m.dbs[deploymentID] = db
	return db, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, db := range m.dbs {
		db.Close()
	}
	m.dbs = make(map[string]*sql.DB)
}

// CloseAll closes all cached database connections, flushing WAL data.
// After this call, connections will be lazily reopened on next access.
// Use before backup to ensure WAL data is flushed to main files.
func (m *Manager) CloseAll() {
	m.Close()
}

func (m *Manager) DeleteDB(deploymentID string) error {
	m.mu.Lock()
	if db, ok := m.dbs[deploymentID]; ok {
		db.Close()
		delete(m.dbs, deploymentID)
	}
	m.mu.Unlock()

	dbPath := filepath.Join(m.persistDir, deploymentID, "store.db")
	os.Remove(dbPath)
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")
	return nil
}

func (m *Manager) checkDBSize(deploymentID string) error {
	dbPath := filepath.Join(m.persistDir, deploymentID, "store.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		return nil // file doesn't exist yet, that's fine
	}
	if info.Size() > maxDBSize {
		return fmt.Errorf("database size limit exceeded (max %d MB)", maxDBSize/(1024*1024))
	}
	return nil
}

func (m *Manager) ListCollections(deployID string) ([]CollectionInfo, error) {
	db, err := m.getDB(deployID)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query("SELECT collection, COUNT(*) FROM documents GROUP BY collection ORDER BY collection")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []CollectionInfo
	for rows.Next() {
		var c CollectionInfo
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, nil
}

func (m *Manager) CreateDocument(deployID, collection string, data json.RawMessage) (*Document, error) {
	if len(data) > maxDocSize {
		return nil, fmt.Errorf("document too large (max %d KB)", maxDocSize/1024)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid JSON data")
	}
	if err := m.checkDBSize(deployID); err != nil {
		return nil, err
	}

	db, err := m.getDB(deployID)
	if err != nil {
		return nil, err
	}

	// Check collection count limit
	var collCount int
	db.QueryRow("SELECT COUNT(DISTINCT collection) FROM documents").Scan(&collCount)

	// Check if this is a new collection
	var existsInCollection int
	db.QueryRow("SELECT COUNT(*) FROM documents WHERE collection = ? LIMIT 1", collection).Scan(&existsInCollection)
	if existsInCollection == 0 && collCount >= maxCollections {
		return nil, fmt.Errorf("collection limit exceeded (max %d)", maxCollections)
	}

	// Check docs per collection limit
	var docCount int
	db.QueryRow("SELECT COUNT(*) FROM documents WHERE collection = ?", collection).Scan(&docCount)
	if docCount >= maxDocsPerCollection {
		return nil, fmt.Errorf("document limit per collection exceeded (max %d)", maxDocsPerCollection)
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	doc := &Document{
		ID:         xid.New().String(),
		Collection: collection,
		Data:       data,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	_, err = db.Exec(
		"INSERT INTO documents (id, collection, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		doc.ID, doc.Collection, string(doc.Data), doc.CreatedAt, doc.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	return doc, nil
}

func (m *Manager) ListDocuments(deployID, collection string, limit, offset int) ([]Document, int, error) {
	db, err := m.getDB(deployID)
	if err != nil {
		return nil, 0, err
	}

	var total int
	db.QueryRow("SELECT COUNT(*) FROM documents WHERE collection = ?", collection).Scan(&total)

	if limit <= 0 || limit > 100 {
		limit = 100
	}

	rows, err := db.Query(
		"SELECT id, collection, data, created_at, updated_at FROM documents WHERE collection = ? ORDER BY created_at DESC LIMIT ? OFFSET ?",
		collection, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		var data string
		if err := rows.Scan(&d.ID, &d.Collection, &data, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, 0, err
		}
		d.Data = json.RawMessage(data)
		docs = append(docs, d)
	}
	return docs, total, nil
}

func (m *Manager) GetDocument(deployID, collection, docID string) (*Document, error) {
	db, err := m.getDB(deployID)
	if err != nil {
		return nil, err
	}

	var d Document
	var data string
	err = db.QueryRow(
		"SELECT id, collection, data, created_at, updated_at FROM documents WHERE id = ? AND collection = ?",
		docID, collection,
	).Scan(&d.ID, &d.Collection, &data, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Data = json.RawMessage(data)
	return &d, nil
}

func (m *Manager) UpdateDocument(deployID, collection, docID string, data json.RawMessage) (*Document, error) {
	if len(data) > maxDocSize {
		return nil, fmt.Errorf("document too large (max %d KB)", maxDocSize/1024)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid JSON data")
	}

	db, err := m.getDB(deployID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	res, err := db.Exec(
		"UPDATE documents SET data = ?, updated_at = ? WHERE id = ? AND collection = ?",
		string(data), now, docID, collection,
	)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil
	}

	return m.GetDocument(deployID, collection, docID)
}

func (m *Manager) DeleteDocument(deployID, collection, docID string) error {
	db, err := m.getDB(deployID)
	if err != nil {
		return err
	}

	res, err := db.Exec("DELETE FROM documents WHERE id = ? AND collection = ?", docID, collection)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}

func (m *Manager) DeleteCollection(deployID, collection string) (int64, error) {
	db, err := m.getDB(deployID)
	if err != nil {
		return 0, err
	}

	res, err := db.Exec("DELETE FROM documents WHERE collection = ?", collection)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
