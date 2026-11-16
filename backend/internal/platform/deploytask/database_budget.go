package deploytask

import (
	"encoding/json"
	"errors"
	"puppeteer/backend/internal/platform/resourcebudget"
	"sort"
	"strings"
)

// Only installer-owned -c settings can be replaced. Other PostgreSQL flags,
// including WAL archiving, are retained byte-for-byte and in their original order.
func databaseCommand(raw json.RawMessage, owned bool) ([]string, error) {
	command := []string{"postgres"}
	if len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &command) != nil || len(command) == 0 || command[0] != "postgres" {
			return nil, errors.New("自定义 PostgreSQL 启动命令不能自动调整")
		}
	}
	kept := []string{"postgres"}
	budget, _ := resourcebudget.Database("light")
	keys := budget.Parameters()
	for i := 1; i < len(command); i += 2 {
		if i+1 >= len(command) || command[i] != "-c" || !strings.Contains(command[i+1], "=") {
			return nil, errors.New("PostgreSQL 参数必须使用独立的 -c name=value")
		}
		name := strings.SplitN(command[i+1], "=", 2)[0]
		if _, ok := keys[name]; ok {
			if !owned {
				return nil, errors.New("存在自定义数据库内存或连接参数，请先合并配置")
			}
			continue
		}
		kept = append(kept, command[i], command[i+1])
	}
	return kept, nil
}
func applyDatabaseBudget(doc map[string]json.RawMessage, m composeModel, name string) error {
	if name == "" {
		return nil
	}
	b, err := resourcebudget.Database(name)
	if err != nil {
		return err
	}
	var version int
	_ = json.Unmarshal(doc["x-puppeteer-database-budget"], &version)
	command, err := databaseCommand(m.Services["postgres"]["command"], version == 1)
	if err != nil {
		return err
	}
	params := b.Parameters()
	keys := []string{}
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		command = append(command, "-c", k+"="+params[k])
	}
	m.Services["postgres"]["command"] = jsonValue(command)
	doc["x-puppeteer-database-budget"] = jsonValue(1)
	return nil
}
