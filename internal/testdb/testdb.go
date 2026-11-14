// Package testdb 给集成测试准备数据库：在一个临时 schema 里跑完迁移，测试结束后整个删掉，不碰库里已有的数据。
//
// 默认不跑（go test ./... 会跳过这些测试）。要跑的话二选一：
//
//	PINGOO_TEST_DATABASE_URL=postgres://… go test ./...   # 指定一个库
//	make test-db                                           # 用项目 .env 里配置的库
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"pingoo/internal/config"
	"pingoo/internal/db"
)

// dsn 返回测试用的连接串；没有配置时返回空。
func dsn(t *testing.T) string {
	if v := os.Getenv("PINGOO_TEST_DATABASE_URL"); v != "" {
		return v
	}
	if os.Getenv("PINGOO_TEST_DB") == "" {
		return ""
	}
	// 用项目 .env 里的数据库：从当前包的目录往上找到 go.mod 所在的目录，在那里读配置
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := wd
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("找不到项目根目录（go.mod）")
		}
		root = parent
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if os.Getenv("ADMIN_EMAIL") == "" {
		os.Setenv("ADMIN_EMAIL", "admin@pingoo.test") // 测试只需要数据库连接，管理员邮箱随便填一个
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("读取 .env 失败: %v", err)
	}
	return cfg.DatabaseURL
}

// Open 返回一个连到全新临时 schema 的连接池（迁移已经跑完）。没有配置测试数据库时跳过当前测试。
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := dsn(t)
	if url == "" {
		t.Skip("没有配置测试数据库（设置 PINGOO_TEST_DATABASE_URL，或运行 make test-db）")
	}
	ctx := context.Background()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	schema := "pingoo_test_" + hex.EncodeToString(b)
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("无法创建临时 schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("临时 schema %s 没有删掉: %v", schema, err)
		}
		admin.Close()
	})
	pc, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// 再确认一遍确实在临时 schema 里：万一 search_path 没生效，宁可停下也不能动到真实的表
	var cur string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&cur); err != nil || cur != schema {
		t.Fatalf("没有进入临时 schema（当前是 %q），停止: %v", cur, err)
	}
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return pool
}

// User 在测试库里建一个普通用户，返回它的 ID（全新 schema 里第一个用户就是 1 号）。
func User(t *testing.T, pool *pgxpool.Pool, email string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO users(email) VALUES ($1) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatalf("建测试用户失败: %v", err)
	}
	return id
}
