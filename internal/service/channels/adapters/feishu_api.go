package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	channelcontract "github.com/nexus-research-lab/nexus/internal/service/channels/contract"
	channelmessage "github.com/nexus-research-lab/nexus/internal/service/channels/message"
	channeltransport "github.com/nexus-research-lab/nexus/internal/service/channels/transport"
)

type feishuMessageEnvelope struct {
	Code int                       `json:"code"`
	Msg  string                    `json:"msg"`
	Data feishuMessageResponseData `json:"data,omitempty"`
}

type feishuMessageResponseData struct {
	MessageID  string `json:"message_id,omitempty"`
	RootID     string `json:"root_id,omitempty"`
	ParentID   string `json:"parent_id,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	ReactionID string `json:"reaction_id,omitempty"`
}

func (c *FeishuChannel) SendDeliveryMessage(ctx context.Context, target channelcontract.DeliveryTarget, text string) (channelcontract.DeliveryResult, error) {
	normalized := target.Normalized()
	if strings.TrimSpace(target.To) == "" {
		return channelcontract.DeliveryResult{}, fmt.Errorf("feishu delivery target requires to")
	}
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return channelcontract.DeliveryResult{}, err
	}
	receiveIDType := normalizeFeishuReceiveIDType(target.AccountID)
	parts := make([]channelmessage.ReceiptPart, 0)
	for _, chunk := range channeltransport.SplitText(strings.TrimSpace(text), 4500) {
		messageID := ""
		if strings.TrimSpace(target.ThreadID) != "" {
			messageID, err = c.replyChunk(ctx, token, target.ThreadID, chunk)
		} else {
			messageID, err = c.sendChunk(ctx, token, receiveIDType, target.To, chunk)
		}
		if err != nil {
			c.clearTenantAccessToken()
			return channelcontract.DeliveryResult{}, err
		}
		if strings.TrimSpace(messageID) != "" {
			parts = append(parts, channelmessage.TextPart(messageID))
		}
	}
	return channelcontract.NewDeliveryResult(normalized, channelmessage.NewReceipt(channelmessage.ReceiptParams{
		Channel:  channelcontract.ChannelTypeFeishu,
		Target:   target.To,
		ThreadID: target.ThreadID,
		Parts:    parts,
	})), nil
}

func (c *FeishuChannel) SendDeliveryTyping(ctx context.Context, target channelcontract.DeliveryTarget, active bool) error {
	messageID := strings.TrimSpace(target.ThreadID)
	if messageID == "" {
		return nil
	}
	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	if active {
		reactionID, err := c.addMessageReaction(ctx, token, messageID, "Typing")
		if err != nil {
			return nil
		}
		if strings.TrimSpace(reactionID) == "" {
			return nil
		}
		c.mu.Lock()
		if c.typingReacts == nil {
			c.typingReacts = make(map[string]string)
		}
		c.typingReacts[messageID] = reactionID
		c.mu.Unlock()
		return nil
	}

	c.mu.Lock()
	reactionID := ""
	if c.typingReacts != nil {
		reactionID = c.typingReacts[messageID]
		delete(c.typingReacts, messageID)
	}
	c.mu.Unlock()
	if reactionID == "" {
		return nil
	}
	_ = c.deleteMessageReaction(ctx, token, messageID, reactionID)
	return nil
}

// feishuPostContent 将 Markdown 文本包装为飞书富文本 post 消息的 content 字段。
// 采用 md 标签以支持 CommonMark + GFM 语法渲染（加粗、列表、代码块、表格等）。
// 若文本开头包含一级标题(# )，则提取为富文本标题并从正文中移除该行。
func feishuPostContent(text string) (string, error) {
	title, body := extractFeishuPostTitle(text)
	post := map[string]any{
		"content": [][]map[string]string{
			{{"tag": "md", "text": body}},
		},
	}
	if title != "" {
		post["title"] = title
	}
	content := map[string]any{
		"zh_cn": post,
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// extractFeishuPostTitle 从 Markdown 文本前三行内解析首个一级标题(# )作为富文本标题。
// 扫描范围限定为文本起始的三行（含空行）；返回标题文本与移除标题行后的正文，无匹配则标题为空。
func extractFeishuPostTitle(text string) (string, string) {
	lines := strings.Split(text, "\n")
	scanLimit := 3
	if len(lines) < scanLimit {
		scanLimit = len(lines)
	}
	for i := 0; i < scanLimit; i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		if !strings.HasPrefix(trimmed, "# ") {
			continue
		}
		title := strings.TrimSpace(trimmed[2:])
		remaining := append(append([]string{}, lines[:i]...), lines[i+1:]...)
		body := strings.Join(remaining, "\n")
		// 移除标题行后的前导换行，避免正文开头多余空行
		body = strings.TrimLeft(body, "\n")
		return title, body
	}
	return "", text
}

func (c *FeishuChannel) sendChunk(ctx context.Context, token string, receiveIDType string, receiveID string, text string) (string, error) {
	content, err := feishuPostContent(text)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]string{
		"receive_id": strings.TrimSpace(receiveID),
		"msg_type":   "post",
		"content":    content,
	})
	if err != nil {
		return "", err
	}
	endpoint := strings.TrimRight(c.baseURL, "/") +
		"/open-apis/im/v1/messages?receive_id_type=" +
		url.QueryEscape(receiveIDType)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	request.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(request)
	if err != nil {
		return "", err
	}
	var envelope feishuMessageEnvelope
	if err = decodeFeishuEnvelope(response, &envelope); err != nil {
		return "", err
	}
	if envelope.Code != 0 {
		return "", fmt.Errorf("feishu send message failed: code=%d msg=%s", envelope.Code, strings.TrimSpace(envelope.Msg))
	}
	return strings.TrimSpace(envelope.Data.MessageID), nil
}

func (c *FeishuChannel) replyChunk(ctx context.Context, token string, messageID string, text string) (string, error) {
	content, err := feishuPostContent(text)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"msg_type": "post",
		"content":  content,
	}
	if c.replyInThread {
		payload["reply_in_thread"] = true
	}
	endpoint := strings.TrimRight(c.baseURL, "/") +
		"/open-apis/im/v1/messages/" +
		url.PathEscape(strings.TrimSpace(messageID)) +
		"/reply"
	var envelope feishuMessageEnvelope
	if err = c.doFeishuJSON(ctx, http.MethodPost, token, endpoint, payload, &envelope); err != nil {
		return "", err
	}
	if envelope.Code != 0 {
		return "", fmt.Errorf("feishu reply message failed: code=%d msg=%s", envelope.Code, strings.TrimSpace(envelope.Msg))
	}
	return strings.TrimSpace(envelope.Data.MessageID), nil
}

func (c *FeishuChannel) addMessageReaction(ctx context.Context, token string, messageID string, emojiType string) (string, error) {
	payload := map[string]any{
		"reaction_type": map[string]string{
			"emoji_type": strings.TrimSpace(emojiType),
		},
	}
	endpoint := strings.TrimRight(c.baseURL, "/") +
		"/open-apis/im/v1/messages/" +
		url.PathEscape(strings.TrimSpace(messageID)) +
		"/reactions"
	var envelope feishuMessageEnvelope
	if err := c.doFeishuJSON(ctx, http.MethodPost, token, endpoint, payload, &envelope); err != nil {
		return "", err
	}
	if envelope.Code != 0 {
		return "", fmt.Errorf("feishu add reaction failed: code=%d msg=%s", envelope.Code, strings.TrimSpace(envelope.Msg))
	}
	return strings.TrimSpace(envelope.Data.ReactionID), nil
}

func (c *FeishuChannel) deleteMessageReaction(ctx context.Context, token string, messageID string, reactionID string) error {
	endpoint := strings.TrimRight(c.baseURL, "/") +
		"/open-apis/im/v1/messages/" +
		url.PathEscape(strings.TrimSpace(messageID)) +
		"/reactions/" +
		url.PathEscape(strings.TrimSpace(reactionID))
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	var envelope feishuMessageEnvelope
	if err = decodeFeishuEnvelope(response, &envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		return fmt.Errorf("feishu delete reaction failed: code=%d msg=%s", envelope.Code, strings.TrimSpace(envelope.Msg))
	}
	return nil
}

func (c *FeishuChannel) doFeishuJSON(
	ctx context.Context,
	method string,
	token string,
	endpoint string,
	payload any,
	target any,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	return decodeFeishuEnvelope(response, target)
}

func decodeFeishuEnvelope(response *http.Response, target any) error {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("feishu request failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	if err = json.Unmarshal(body, target); err != nil {
		return err
	}
	return nil
}

func normalizeFeishuReceiveIDType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "chat", "group", "chat_id":
		return "chat_id"
	case "open_id", "union_id", "user_id", "email":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return strings.TrimSpace(value)
	}
}
