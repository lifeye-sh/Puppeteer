package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"filippo.io/age"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/fault"
	"regexp"
)

func (a *App) routesBackupRecipient(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("POST /api/admin/backup/recipient-challenge", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "backup.export"); e != nil {
			return e
		}
		var in struct {
			Recipient string `json:"recipient"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		recipient, e := age.ParseX25519Recipient(in.Recipient)
		if e != nil {
			return fault.Invalid
		}
		response := identity.NewToken()
		var encrypted bytes.Buffer
		writer, e := age.Encrypt(&encrypted, recipient)
		if e != nil {
			return e
		}
		if _, e = writer.Write([]byte(response)); e != nil {
			return e
		}
		if e = writer.Close(); e != nil {
			return e
		}
		var id string
		e = a.db.Within(r.Context(), func(c context.Context) error {
			if _, e := a.db.Exec(c, `DELETE FROM backup_recipient_proofs WHERE expires_at<now() OR session_hash=$1`, identity.Digest(token(r))); e != nil {
				return e
			}
			return a.db.Row(c, `INSERT INTO backup_recipient_proofs(session_hash,recipient,response_hash) VALUES($1,$2,$3) RETURNING id::text`, identity.Digest(token(r)), in.Recipient, identity.Digest(response)).Scan(&id)
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id, "challenge": base64.StdEncoding.EncodeToString(encrypted.Bytes())})
		return nil
	})
}
func (a *App) verifyRecipient(c context.Context, r *http.Request, recipient, id, response string) error {
	var previous string
	if e := a.db.Row(c, `SELECT recipient FROM backup_config WHERE singleton=true FOR UPDATE`).Scan(&previous); e != nil {
		return e
	}
	if previous == recipient {
		return nil
	}
	if !regexp.MustCompile(`^[a-fA-F0-9]{8}(-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}$`).MatchString(id) || len(response) > 128 {
		return problem(422, "backup_recipient_proof_required", "请先完成恢复私钥离线验证")
	}
	tag, e := a.db.Exec(c, `DELETE FROM backup_recipient_proofs WHERE id=$1 AND session_hash=$2 AND recipient=$3 AND response_hash=$4 AND expires_at>now()`, id, identity.Digest(token(r)), recipient, identity.Digest(response))
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return problem(422, "backup_recipient_proof_invalid", "恢复私钥验证失败或已过期，请重新生成验证文件")
	}
	return nil
}
