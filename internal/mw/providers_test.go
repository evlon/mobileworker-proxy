package mw

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStripJSONC(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"行注释", `{"a": 1} // 注释`, `{"a": 1} `},
		{"块注释", `{"a": /* 中间 */ 1}`, `{"a":  1}`},
		{"字符串内的//", `{"url": "http://x/p"}`, `{"url": "http://x/p"}`},
		{"转义引号", `{"a": "he said \"//not comment\""}`, `{"a": "he said \"//not comment\""}`},
		{"多行", "{\n  // 头部注释\n  \"a\": 1\n}", "{\n  \n  \"a\": 1\n}"},
		{"行尾注释保留换行", "{\"a\":1}// x\n", "{\"a\":1}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripJSONC(c.in)
			if got != c.want {
				t.Errorf("stripJSONC(%q)\n 得到 %q\n 期望 %q", c.in, got, c.want)
			}
		})
	}
}

// 用临时目录模拟 MobileWork 的运行时文件。
func TestReadPortAndIndex(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOBILEWORK_DIR", dir)

	if err := os.WriteFile(filepath.Join(dir, "model-proxy-port"), []byte("60290\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := ReadPort()
	if err != nil || n != 60290 {
		t.Fatalf("ReadPort = %d, %v; 期望 60290", n, err)
	}

	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := `{
	  // 这是注释
	  "provider": {
	    "mw-auto": {
	      "name": "智能推荐",
	      "options": { "baseURL": "http://127.0.0.1:60290/p/mw-auto", "apiKey": "proxy" },
	      "models": {
	        "auto": { "name": "自动", "limit": { "context": 196608, "input": 177287, "output": 32768 } }
	      }
	    },
	    "moma": {
	      "name": "MOMA",
	      "options": { "baseURL": "http://127.0.0.1:60290/p/moma" },
	      "models": {
	        "dsv4.1-flash": {
	          "name": "dsv4.1-flash",
	          "limit": { "context": 389120, "output": 65536 },
	          "modalities": { "input": ["text", "image"] }
	        }
	      }
	    }
	  }
	}`
	if err := os.WriteFile(filepath.Join(cfgDir, "providers.jsonc"), []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("应有 2 个模型，实际 %d", len(entries))
	}
	// 按模型名排序
	if entries[0].Model != "auto" || entries[1].Model != "dsv4.1-flash" {
		t.Errorf("排序不对: %v", entries)
	}
	if entries[0].ProviderID != "mw-auto" || entries[0].Label != "自动" || entries[0].Context != 196608 {
		t.Errorf("auto 解析不对: %+v", entries[0])
	}
	if entries[1].Output != 65536 || len(entries[1].Modalities) != 2 {
		t.Errorf("dsv4.1-flash 解析不对: %+v", entries[1])
	}

	base, err := UpstreamBase()
	if err != nil || base != "http://127.0.0.1:60290" {
		t.Errorf("UpstreamBase = %q, %v", base, err)
	}
}

// 端口文件缺失时，回退到 providers.jsonc 的 baseURL 根。
func TestUpstreamBaseFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOBILEWORK_DIR", dir)
	cfgDir := filepath.Join(dir, "config")
	os.MkdirAll(cfgDir, 0o755)
	jsonc := `{"provider":{"p":{"options":{"baseURL":"http://127.0.0.1:1234/p/p"},"models":{}}}}`
	os.WriteFile(filepath.Join(cfgDir, "providers.jsonc"), []byte(jsonc), 0o644)

	base, err := UpstreamBase()
	if err != nil || base != "http://127.0.0.1:1234" {
		t.Errorf("回退应得到 http://127.0.0.1:1234，实际 %q, %v", base, err)
	}
}

// 端口文件内容非法时应报错。
func TestReadPortInvalid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOBILEWORK_DIR", dir)
	os.WriteFile(filepath.Join(dir, "model-proxy-port"), []byte("not-a-number"), 0o644)
	if _, err := ReadPort(); err == nil {
		t.Error("非法端口内容应当报错")
	}
	os.WriteFile(filepath.Join(dir, "model-proxy-port"), []byte("99999"), 0o644)
	if _, err := ReadPort(); err == nil {
		t.Error("越界端口应当报错")
	}
}
