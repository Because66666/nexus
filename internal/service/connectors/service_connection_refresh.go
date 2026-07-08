package connectors

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nexus-research-lab/nexus/internal/connectors/providers"
)

func (s *Service) refreshActiveConnectionIfNeeded(ctx context.Context, ownerUserID string, record connectionRecord) (connectionRecord, error) {
	if record.ConnectorID != "feishu-docx" {
		return record, nil
	}
	payload, err := s.connectionCredentialsPayload(record)
	if err != nil {
		return record, err
	}
	current, err := credentialMapFromPayload(payload)
	if err != nil {
		return record, err
	}
	// tenant_access_token 未过期则直接返回
	if !credentialNeedsRefresh(current) {
		return record, nil
	}
	appID := strings.TrimSpace(current["app_id"])
	appSecret := strings.TrimSpace(current["app_secret"])
	if appID == "" || appSecret == "" {
		// 从 OAuthClient 表补充凭证（兼容旧配置）
		clientID, clientSecret, configErr := s.oauthCredentials(ctx, ownerUserID, record.ConnectorID)
		if configErr != nil {
			return record, nil
		}
		appID, appSecret = clientID, clientSecret
	}
	if appID == "" || appSecret == "" {
		return record, nil
	}
	provider, err := providers.Get(record.ConnectorID)
	if err != nil {
		return record, err
	}
	feishuProvider, ok := provider.(interface {
		TenantToken(ctx context.Context, httpClient *http.Client, appID, appSecret string) (string, time.Time, error)
	})
	if !ok {
		return record, nil
	}
	newToken, expiresAt, err := feishuProvider.TenantToken(ctx, s.httpClient, appID, appSecret)
	if err != nil {
		return record, err
	}
	if current == nil {
		current = map[string]string{}
	}
	current["app_id"] = appID
	current["app_secret"] = appSecret
	current["tenant_access_token"] = newToken
	current["expires_at"] = formatExpiresAt(expiresAt)
	encoded, err := json.Marshal(current)
	if err != nil {
		return record, err
	}
	record.Credentials = string(encoded)
	record.CredentialsEncrypted = sql.NullString{}
	if err = s.upsertConnection(ctx, connectionRecord{
		OwnerUserID: ownerUserID,
		ConnectorID: record.ConnectorID,
		State:       "connected",
		Credentials: record.Credentials,
		AuthType:    record.AuthType,
	}); err != nil {
		return record, err
	}
	return record, nil
}

func formatExpiresAt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return strconv.FormatInt(t.Unix(), 10)
}
