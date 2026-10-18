// main.go — go-duckdb (CGO) 集成最小验证（issue #59 验收项 2）
//
// 目标：验证生产集成路径——CGO 工具链下编译 go-duckdb、打开基准库文件、
// 跑通一条风险场景查询。这验证的是「本项目工具链能不能用 DuckDB」，
// 而非引擎性能（性能由 bench.py 裁决）。
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2"
)

func main() {
	dbPath := os.Args[1]
	dsn := dbPath + "?access_mode=read_only&threads=2"
	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer db.Close()

	start := time.Now()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM electricity_fees").Scan(&n); err != nil {
		log.Fatalf("count: %v", err)
	}
	fmt.Printf("go-duckdb CGO OK: electricity_fees = %d rows (%.3fs)\n", n, time.Since(start).Seconds())

	// 一条真实风险场景查询（Q1 退租站仍在计费）
	start = time.Now()
	rows, err := db.Query(`
		SELECT COUNT(*), ROUND(SUM(e.amount),2) FROM electricity_fees e
		JOIN terminated_sites t ON e.site_code = t.site_code
		WHERE (e.year*12+e.month) > (t.term_year*12+t.term_month)`)
	if err != nil {
		log.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hitRows int
		var hitAmount float64
		if err := rows.Scan(&hitRows, &hitAmount); err != nil {
			log.Fatalf("scan: %v", err)
		}
		fmt.Printf("Q1 退租站仍在计费: %d 行, 金额 %.2f (%.3fs)\n", hitRows, hitAmount, time.Since(start).Seconds())
	}
	fmt.Println("GO-DUCKDB-VALIDATION-OK")
}
