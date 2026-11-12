package pgproxy

import (
	"encoding/json"
	"errors"
	"strings"

	pgquery "github.com/wasilibs/go-pgquery"
)

var ErrSQLDenied = errors.New("SQL is outside the PostgreSQL readonly profile")
var parseSlots = make(chan struct{}, 2)

// This is an AST policy, backed by a restricted target role and readonly session.
// Unknown functions/types and parser syntax are denied, never prefix-matched.
func ValidateSQL(sql string) error {
	if len(sql) > 1<<20 || strings.ContainsRune(sql, 0) {
		return ErrSQLDenied
	}
	parseSlots <- struct{}{}
	defer func() { <-parseSlots }()
	raw, e := pgquery.ParseToJSON(sql)
	if e != nil {
		return ErrSQLDenied
	}
	var tree struct {
		Statements []struct {
			Statement map[string]any `json:"stmt"`
		} `json:"stmts"`
	}
	if json.Unmarshal([]byte(raw), &tree) != nil || len(tree.Statements) > 1 {
		return ErrSQLDenied
	}
	if len(tree.Statements) == 0 {
		return nil
	}
	stmt := tree.Statements[0].Statement
	for key, val := range stmt {
		v, _ := val.(map[string]any)
		switch key {
		case "SelectStmt":
			return walkReadOnly(stmt)
		case "VariableShowStmt":
			return nil
		case "VariableSetStmt":
			if v["kind"] == "VAR_SET_MULTI" && (v["name"] == "SESSION CHARACTERISTICS" || v["name"] == "TRANSACTION") {
				return transactionOptions(v["args"])
			}
			if v["kind"] != "VAR_SET_VALUE" {
				return ErrSQLDenied
			}
			name, _ := v["name"].(string)
			switch name {
			case "application_name", "client_encoding", "extra_float_digits", "DateStyle", "datestyle", "TimeZone", "timezone":
			default:
				return ErrSQLDenied
			}
			args, _ := v["args"].([]any)
			if len(args) != 1 {
				return ErrSQLDenied
			}
			// Values must be constants, never expressions or DEFAULT/RESET.
			arg, _ := args[0].(map[string]any)
			if _, ok := arg["A_Const"]; !ok {
				return ErrSQLDenied
			}
			if name == "client_encoding" && !strings.Contains(strings.ToUpper(raw), `"UTF8"`) && !strings.Contains(strings.ToUpper(raw), `"UTF-8"`) {
				return ErrSQLDenied
			}
			return nil
		case "TransactionStmt":
			kind, _ := v["kind"].(string)
			switch kind {
			case "TRANS_STMT_BEGIN", "TRANS_STMT_START", "TRANS_STMT_COMMIT", "TRANS_STMT_ROLLBACK", "TRANS_STMT_SAVEPOINT", "TRANS_STMT_RELEASE", "TRANS_STMT_ROLLBACK_TO":
			default:
				return ErrSQLDenied
			}
			return transactionOptions(v["options"])
		case "CopyStmt":
			if v["is_from"] == true || v["is_program"] == true || v["filename"] != nil {
				return ErrSQLDenied
			}
			if _, ok := v["query"]; ok {
				return walkReadOnly(v["query"])
			}
			if _, ok := v["relation"]; ok {
				return nil
			}
		}
	}
	return ErrSQLDenied
}
func stringNames(v any) []string {
	list, _ := v.([]any)
	out := []string{}
	for _, x := range list {
		m, _ := x.(map[string]any)
		s, _ := m["String"].(map[string]any)
		n, _ := s["sval"].(string)
		out = append(out, n)
	}
	return out
}
func allowedName(v any, allowed string) bool {
	names := stringNames(v)
	if len(names) == 2 && names[0] == "pg_catalog" {
		names = names[1:]
	}
	if len(names) != 1 {
		return false
	}
	for _, candidate := range strings.Split(allowed, "|") {
		if strings.ToLower(names[0]) == candidate {
			return true
		}
	}
	return false
}

// JDBC uses BEGIN READ ONLY and session isolation settings. Reject READ WRITE,
// duplicate options, expressions and unknown options rather than relaxing defaults.
func transactionOptions(value any) error {
	if value == nil {
		return nil
	}
	options, ok := value.([]any)
	if !ok {
		return ErrSQLDenied
	}
	seen := map[string]bool{}
	for _, option := range options {
		wrapper, _ := option.(map[string]any)
		def, _ := wrapper["DefElem"].(map[string]any)
		name, _ := def["defname"].(string)
		if seen[name] {
			return ErrSQLDenied
		}
		seen[name] = true
		arg, _ := def["arg"].(map[string]any)
		constant, _ := arg["A_Const"].(map[string]any)
		switch name {
		case "transaction_read_only":
			number, _ := constant["ival"].(map[string]any)
			if number["ival"] != float64(1) {
				return ErrSQLDenied
			}
		case "transaction_isolation":
			str, _ := constant["sval"].(map[string]any)
			switch str["sval"] {
			case "read committed", "read uncommitted", "repeatable read", "serializable":
			default:
				return ErrSQLDenied
			}
		default:
			return ErrSQLDenied
		}
	}
	return nil
}
func walkReadOnly(node any) error {
	switch v := node.(type) {
	case []any:
		for _, n := range v {
			if e := walkReadOnly(n); e != nil {
				return e
			}
		}
	case map[string]any:
		for k, n := range v {
			if (strings.HasSuffix(k, "Stmt") && k != "SelectStmt") || k == "IntoClause" || k == "intoClause" || k == "LockingClause" || k == "lockingClause" {
				return ErrSQLDenied
			}
			if k == "FuncCall" {
				m, _ := n.(map[string]any)
				if !allowedFunction(m["funcname"], "count|sum|avg|min|max|abs|round|floor|ceil|ceiling|lower|upper|length|char_length|octet_length|substring|substr|concat|concat_ws|replace|trim|btrim|ltrim|rtrim|left|right|now|clock_timestamp|current_database|current_schema|current_schemas|version|pg_backend_pid|pg_sleep|pg_get_userbyid|pg_get_expr|pg_get_indexdef|pg_get_constraintdef|pg_get_viewdef|pg_get_serial_sequence|pg_get_function_arguments|pg_get_function_result|pg_get_function_identity_arguments|pg_table_is_visible|pg_type_is_visible|pg_function_is_visible|pg_encoding_to_char|format_type|quote_ident|quote_literal|has_table_privilege|has_schema_privilege|has_database_privilege|has_column_privilege|has_function_privilege|pg_is_in_recovery|pg_is_other_temp_schema|pg_get_keywords|obj_description|col_description|shobj_description|array_agg|string_agg|json_agg|jsonb_agg|array_length|array_lower|array_upper|unnest|generate_series|row_number|rank|dense_rank|lag|lead|first_value|last_value|to_char|to_date|to_timestamp|date_part|date_trunc|extract") {
					return ErrSQLDenied
				}
			}
			if k == "TypeName" || k == "typeName" {
				m, _ := n.(map[string]any)
				if !allowedName(m["names"], "int2|int4|int8|integer|smallint|bigint|numeric|decimal|float4|float8|real|double precision|bool|boolean|text|varchar|bpchar|char|name|oid|regclass|regtype|regnamespace|regproc|date|timestamp|timestamptz|time|timetz|interval|uuid|json|jsonb|bytea") {
					return ErrSQLDenied
				}
			}
			if k == "A_Expr" {
				m, _ := n.(map[string]any)
				names := stringNames(m["name"])
				if len(names) == 2 && names[0] == "pg_catalog" {
					names = names[1:]
				}
				concat := false
				if len(names) == 1 {
					switch names[0] {
					case "||", "|", "&", "#", "<<", ">>":
						concat = true
					}
				}
				if !concat && !allowedName(m["name"], "=|<>|!=|<|>|<=|>=|+|-|*|/|%|^|~~|!~~|~~*|!~~*|~|!~|~*|!~*|@>|<@|&&|->|->>|#>|#>>|between|not between|between symmetric|not between symmetric") {
					return ErrSQLDenied
				}
			}
			if e := walkReadOnly(n); e != nil {
				return e
			}
		}
	}
	return nil
}

// pgJDBC uses this built-in information_schema helper for key ordering.
func allowedFunction(v any, allowed string) bool {
	names := stringNames(v)
	if len(names) == 2 && names[0] == "information_schema" && names[1] == "_pg_expandarray" {
		return true
	}
	return allowedName(v, allowed)
}
