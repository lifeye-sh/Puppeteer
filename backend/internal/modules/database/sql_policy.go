package database

import (
	"errors"
	"strconv"
	"strings"
	"vitess.io/vitess/go/vt/sqlparser"
)

var ErrSQLDenied = errors.New("SQL is outside the supported read-only profile")

const MaxSQLBytes = 1 << 20

type SQLPolicy struct{ parser *sqlparser.Parser }

func NewSQLPolicy() (*SQLPolicy, error) {
	p, e := sqlparser.New(sqlparser.Options{MySQLServerVersion: "8.4.0"})
	return &SQLPolicy{parser: p}, e
}

// ReadOnly is a conservative AST policy, not a substitute for target grants.
// Every proxy connection must additionally use a genuine SELECT-only account.
func (p *SQLPolicy) ReadOnly(query string) error {
	return p.readOnly(query, "")
}

// ReadOnlyForDatabase also permits clients to select this exact database using
// SQL USE (Connector/J), in addition to the protocol COM_INIT_DB command.
func (p *SQLPolicy) ReadOnlyForDatabase(query, database string) error {
	return p.readOnly(query, database)
}
func (p *SQLPolicy) readOnly(query, database string) error {
	if len(query) == 0 || len(query) > MaxSQLBytes || strings.Contains(query, "\x00") || strings.Contains(query, "/*!") {
		return ErrSQLDenied
	}
	statement, e := p.parser.Parse(query)
	if e != nil {
		return ErrSQLDenied
	}
	switch statement.(type) {
	case *sqlparser.Use:
		if database == "" || statement.(*sqlparser.Use).DBName.String() != database {
			return ErrSQLDenied
		}
	case *sqlparser.Select, *sqlparser.Union:
	case *sqlparser.Begin:
		for _, mode := range statement.(*sqlparser.Begin).TxAccessModes {
			switch strings.ToLower(mode.ToString()) {
			case "read only", "with consistent snapshot":
			default:
				return ErrSQLDenied
			}
		}
	case *sqlparser.Commit, *sqlparser.Rollback:
	case *sqlparser.Set:
		for _, expression := range statement.(*sqlparser.Set).Exprs {
			if expression.Var == nil || (expression.Var.Scope != sqlparser.NoScope && expression.Var.Scope != sqlparser.SessionScope) {
				return ErrSQLDenied
			}
			name := strings.ToLower(expression.Var.Name.String())
			value := strings.ToLower(sqlparser.String(expression.Expr))
			value = strings.Trim(value, "'")
			switch name {
			case "sql_select_limit":
				// JDBC setMaxRows changes this session-only result limit.
				if value != "default" {
					literal, ok := expression.Expr.(*sqlparser.Literal)
					if !ok || literal.Type != sqlparser.IntVal {
						return ErrSQLDenied
					}
					if _, err := strconv.ParseUint(literal.Val, 10, 64); err != nil {
						return ErrSQLDenied
					}
				}
			case "names", "character_set_client", "character_set_connection", "character_set_results":
				if value != "utf8mb4" && value != "utf8mb3" && value != "utf8" && value != "binary" && value != "null" {
					return ErrSQLDenied
				}
			case "autocommit":
				if value != "0" && value != "1" && value != "on" && value != "off" {
					return ErrSQLDenied
				}
			case "transaction_isolation":
				if value != "read-committed" && value != "repeatable-read" && value != "serializable" && value != "read-uncommitted" {
					return ErrSQLDenied
				}
			default:
				return ErrSQLDenied
			}
		}
	case *sqlparser.Show:
		normalized := strings.ToLower(sqlparser.String(statement))
		ok := false
		for _, prefix := range []string{"show table status", "show engines", "show storage engines", "show plugins", "show tables", "show full tables", "show columns", "show full columns", "show fields", "show index", "show keys", "show databases", "show schemas", "show create table", "show create view", "show variables", "show session variables", "show status", "show session status", "show warnings", "show errors", "show collation", "show charset", "show character set"} {
			if normalized == prefix || strings.HasPrefix(normalized, prefix+" ") {
				ok = true
				break
			}
		}
		if !ok {
			return ErrSQLDenied
		}
	default:
		return ErrSQLDenied
	}
	return sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		switch v := node.(type) {
		case *sqlparser.Select:
			if v.Into != nil || v.Lock != sqlparser.NoLock {
				return false, ErrSQLDenied
			}
		case *sqlparser.Union:
			if v.GetLock() != sqlparser.NoLock {
				return false, ErrSQLDenied
			}
		case *sqlparser.AssignmentExpr, *sqlparser.LockingFunc:
			return false, ErrSQLDenied
		case *sqlparser.FuncExpr:
			if !v.Qualifier.IsEmpty() {
				return false, ErrSQLDenied
			}
			if _, ok := readFunctions[strings.ToLower(v.Name.String())]; !ok {
				return false, ErrSQLDenied
			}
		}
		return true, nil
	}, statement)
}

var readFunctions = func() map[string]struct{} {
	names := strings.Fields("abs acos asin atan atan2 ceil ceiling floor round truncate mod pow power sqrt exp ln log log2 log10 sign pi degrees radians cos sin tan cot crc32 md5 sha sha1 sha2 hex unhex uuid_to_bin bin_to_uuid length char_length character_length bit_length octet_length ascii ord char concat concat_ws coalesce if ifnull nullif greatest least lower lcase upper ucase reverse replace repeat lpad rpad trim ltrim rtrim substring substr left right locate instr strcmp position find_in_set format quote space soundex elt field date time now curdate curtime current_date current_time current_timestamp utc_date utc_time utc_timestamp date_format time_format str_to_date datediff timediff timestampdiff timestampadd date_add date_sub adddate subdate extract year month day dayofmonth dayofweek dayofyear week weekday weekofyear hour minute second microsecond last_day unix_timestamp from_unixtime from_days to_days to_seconds makedate maketime monthname dayname convert_tz database schema version user current_user session_user system_user connection_id found_rows row_count isnull is_ipv4 is_ipv6 inet_aton inet_ntoa inet6_aton inet6_ntoa json_valid json_type json_length json_depth json_extract json_unquote json_quote json_contains json_contains_path json_keys json_search json_object json_array json_pretty json_storage_size json_storage_free")
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}()
