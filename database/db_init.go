package database

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v4"
	"github.com/sirupsen/logrus"
)

type Meson struct {
	ReqID     string
	ChainA    string
	ChainB    string
	Timestamp int64
	AmountA   float64
	AmountB   float64
	ActionA   string
	ActionB   string
	TxHashA   string
	TxHashB   string
	IsCheck   bool
}

var (
	connInstance *pgx.Conn
	connOnce     sync.Once
	connLock     sync.Mutex
)

// Connect 初始化一个 PostgreSQL 客户端实例
func Connect(postgresURI string) error {
	connLock.Lock()
	defer connLock.Unlock()

	if connInstance == nil {
		conn, err := pgx.Connect(context.Background(), postgresURI)
		if err != nil {
			return err
		}
		logrus.Println("Connected to PostgreSQL!")
		connInstance = conn
	}

	return nil
}

// Disconnect 关闭 PostgreSQL 客户端连接
func Disconnect() error {
	connLock.Lock()
	defer connLock.Unlock()

	if connInstance != nil {
		err := connInstance.Close(context.Background())
		if err != nil {
			return err
		}
		connInstance = nil
		logrus.Println("Disconnected from PostgreSQL.")
	}
	return nil
}

// InitDatabase 初始化数据库
func InitDatabase() error {
	conn := connInstance

	createTableQuery := `
	CREATE TABLE IF NOT EXISTS meson (
		reqid TEXT PRIMARY KEY,
		chain_a TEXT,
		chain_b TEXT,
		timestamp BIGINT,
		amount_a FLOAT8,
		amount_b FLOAT8,
		action_a TEXT,
		action_b TEXT,
		tx_hash_a TEXT,
		tx_hash_b TEXT,
		is_check BOOLEAN
	);`
	_, err := conn.Exec(context.Background(), createTableQuery)
	if err != nil {
		return err
	}
	logrus.Println("Table 'meson' is ready.")
	return nil
}

// FindMesonByReqID 根据 reqID 查询 Meson 文档
func FindMesonByReqID(reqID string) (*Meson, error) {
	conn := connInstance

	query := `SELECT reqid, chain_a, chain_b, timestamp, amount_a, amount_b, action_a, action_b, tx_hash_a, tx_hash_b, is_check FROM meson WHERE reqid = $1`
	row := conn.QueryRow(context.Background(), query, reqID)

	var meson Meson
	err := row.Scan(&meson.ReqID, &meson.ChainA, &meson.ChainB, &meson.Timestamp, &meson.AmountA, &meson.AmountB, &meson.ActionA, &meson.ActionB, &meson.TxHashA, &meson.TxHashB, &meson.IsCheck)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &meson, nil
}

// InsertMeson 插入 Meson 文档到 meson 集合
func InsertMeson(meson Meson) error {
	conn := connInstance

	query := `INSERT INTO meson (reqid, chain_a, chain_b, timestamp, amount_a, amount_b, action_a, action_b, tx_hash_a, tx_hash_b, is_check) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	_, err := conn.Exec(context.Background(), query, meson.ReqID, meson.ChainA, meson.ChainB, meson.Timestamp, meson.AmountA, meson.AmountB, meson.ActionA, meson.ActionB, meson.TxHashA, meson.TxHashB, meson.IsCheck)
	if err != nil {
		logrus.Errorf("Failed to insert Meson: %v", err)
		return err
	}

	logrus.Infof("Inserted Meson with ID: %v", meson.ReqID)
	return nil
}

// UpdateMeson 更新 Meson 文档
func UpdateMeson(meson *Meson) error {
	conn := connInstance

	query := `UPDATE meson SET chain_b = $1, amount_b = $2, action_b = $3, tx_hash_b = $4, is_check = $5 WHERE reqid = $6`
	_, err := conn.Exec(context.Background(), query, meson.ChainB, meson.AmountB, meson.ActionB, meson.TxHashB, meson.IsCheck, meson.ReqID)
	if err != nil {
		logrus.Errorf("Failed to update Meson: %v", err)
		return err
	}

	logrus.Infof("Updated Meson with ID: %v", meson.ReqID)
	return nil
}

// FindUncheckedMesons 查询 is_check 为 false 的 Meson 文档
func FindUncheckedMesons() ([]Meson, error) {
	conn := connInstance

	query := `SELECT reqid, chain_a, chain_b, timestamp, amount_a, amount_b, action_a, action_b, tx_hash_a, tx_hash_b, is_check FROM meson WHERE is_check = false`
	rows, err := conn.Query(context.Background(), query)
	if err != nil {
		logrus.Errorf("Failed to find unchecked Mesons: %v", err)
		return nil, err
	}
	defer rows.Close()

	var results []Meson
	for rows.Next() {
		var meson Meson
		err := rows.Scan(&meson.ReqID, &meson.ChainA, &meson.ChainB, &meson.Timestamp, &meson.AmountA, &meson.AmountB, &meson.ActionA, &meson.ActionB, &meson.TxHashA, &meson.TxHashB, &meson.IsCheck)
		if err != nil {
			logrus.Errorf("Failed to decode Meson: %v", err)
			return nil, err
		}
		results = append(results, meson)
	}

	if rows.Err() != nil {
		logrus.Errorf("Rows error: %v", rows.Err())
		return nil, rows.Err()
	}

	return results, nil
}
