package core

import "testing"

// 以下期望值由 Java 21 运行 verify/FlywayChecksum.java 得出，
// 该 Java 程序逐行复刻 flyway-core 的 ChecksumCalculator.calculateChecksumForResource。
//
// 这是本项目"1:1 复刻"的核心证据：checksum 必须与 Flyway 官方实现逐位一致，
// 否则无法与 Java 版 Flyway 共用同一张 flyway_schema_history 表。
var flywayChecksumGolden = map[string]struct {
	content string
	want    int64
}{
	// CREATE TABLE users (\n  id BIGINT PRIMARY KEY,\n  name VARCHAR(100)\n);\n
	"v1_lf": {
		content: "CREATE TABLE users (\n  id BIGINT PRIMARY KEY,\n  name VARCHAR(100)\n);\n",
		want:    -1982280934,
	},
	// 同一内容的 CRLF 版本，官方声明 line-ending independent
	"v2_crlf": {
		content: "CREATE TABLE users (\r\n  id BIGINT PRIMARY KEY,\r\n  name VARCHAR(100)\r\n);\r\n",
		want:    -1982280934,
	},
	// 无结尾换行
	"v3_no_trailing_nl": {
		content: "SELECT 1;",
		want:    78787420,
	},
	// 带 BOM
	"v4_bom": {
		content: "\uFEFFSELECT 1;\nSELECT 2;\n",
		want:    -1665099012,
	},
	// 中文内容
	"v5_cjk": {
		content: "-- 中文注释\nINSERT INTO t VALUES (\"中文\");\n",
		want:    16336969,
	},
	"v6_empty": {
		content: "",
		want:    0,
	},
	"v7_delimiter": {
		content: "DELIMITER $$\nCREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\nEND$$\nDELIMITER ;\n",
		want:    -239523827,
	},
}

func TestChecksumMatchesFlywayGolden(t *testing.T) {
	for name, tc := range flywayChecksumGolden {
		got := Checksum(tc.content)
		if got != tc.want {
			t.Errorf("%s: checksum = %d, 期望 %d（与 Flyway 官方 Java 实现不一致）",
				name, got, tc.want)
		}
	}
}
