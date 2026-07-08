package dm

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/nexus-research-lab/nexus/internal/infra/authctx"
	"github.com/nexus-research-lab/nexus/internal/infra/logx"
	"github.com/nexus-research-lab/nexus/internal/protocol"
	runtimectx "github.com/nexus-research-lab/nexus/internal/runtime"
	workspacestore "github.com/nexus-research-lab/nexus/internal/storage/workspace"
)

func (s *Service) queueRunningInput(
	ctx context.Context,
	sessionKey string,
	agentValue *protocol.Agent,
	sessionItem protocol.Session,
	request Request,
	initialMessageCount int,
) (bool, error) {
	content := strings.TrimSpace(request.Content)
	attachments := s.normalizeChatAttachments(request.Attachments, agentValue.AgentID)
	runningRoundIDs := s.runtime.GetRunningRoundIDs(sessionKey)
	if len(runningRoundIDs) == 0 {
		return false, runtimectx.ErrNoRunningRound
	}

	// 持久化排队：将消息写入 InputQueue，等当前 round 完成后自动派发，
	// 避免流式注入导致 SDK 中断当前运行中的 round。
	location := workspacestore.InputQueueLocation{
		Scope:         protocol.InputQueueScopeDM,
		WorkspacePath: agentValue.WorkspacePath,
		SessionKey:    sessionKey,
	}
	items, err := s.inputQueue.Enqueue(location, protocol.InputQueueItem{
		Scope:               protocol.InputQueueScopeDM,
		SessionKey:          sessionKey,
		AgentID:             agentValue.AgentID,
		Source:              protocol.InputQueueSourceUser,
		Content:             content,
		Attachments:         attachments,
		DeliveryPolicy:      protocol.NormalizeChatDeliveryPolicy(string(request.DeliveryPolicy)),
		ExternalReplyTarget: externalReplyTargetToProtocol(request.ExternalReplyTarget),
		OwnerUserID:         authctx.OwnerUserID(ctx),
	})
	if err != nil {
		return false, err
	}
	if err := s.recordRoundMarkerWithOptions(agentValue.WorkspacePath, sessionItem, request.RoundID, content, workspacestore.RoundMarkerOptions{
		UserMessageID:  request.UserMessageID,
		AgentRoundID:   request.AgentRoundID,
		DeliveryPolicy: string(protocol.ChatDeliveryPolicyQueue),
		Attachments:    attachments,
	}); err != nil {
		s.loggerFor(ctx).Error("DM 排队消息持久化失败",
			"session_key", sessionKey,
			"agent_id", agentValue.AgentID,
			"round_id", request.RoundID,
			"err", err,
		)
		return false, err
	}
	if _, err := s.refreshSessionMetaAfterRoundMarker(agentValue.WorkspacePath, sessionItem); err != nil {
		s.loggerFor(ctx).Error("DM 排队消息刷新 session meta 失败",
			"session_key", sessionKey,
			"agent_id", agentValue.AgentID,
			"round_id", request.RoundID,
			"err", err,
		)
		return false, err
	}
	runtimeProvider, runtimeModel := runtimeSelectionFromSession(sessionItem)
	s.scheduleTitleGeneration(ctx, protocol.ParseSessionKey(sessionKey), sessionItem, content, initialMessageCount, runtimeProvider, runtimeModel)
	s.broadcastEventWithTimeout(ctx, sessionKey, protocol.NewChatAckEvent(
		sessionKey,
		request.ClientRequestID,
		request.ClientMessageID,
		request.RoundID,
		request.UserMessageID,
		nil,
	))
	if request.BroadcastUserMessage {
		s.broadcastUserRoundMarker(ctx, sessionItem, request.RoundID, request.UserMessageID, content, protocol.ChatDeliveryPolicyQueue, attachments)
	}
	s.broadcastSessionStatus(ctx, sessionKey)
	// 广播队列快照，让前端感知待发送队列变更
	s.broadcastInputQueueSnapshot(ctx, sessionKey, items)
	s.loggerFor(ctx).Info("DM 消息已排队等待运行中 round 完成",
		"session_key", sessionKey,
		"agent_id", agentValue.AgentID,
		"round_id", request.RoundID,
		"running_round_ids", runningRoundIDs,
		"content_chars", utf8.RuneCountInString(content),
		"content_preview", logx.PreviewText(content, 240),
	)
	return true, nil
}

func (s *Service) guideRunningInput(
	ctx context.Context,
	sessionKey string,
	agentValue *protocol.Agent,
	sessionItem protocol.Session,
	request Request,
) (bool, error) {
	content := strings.TrimSpace(request.Content)
	attachments := s.normalizeChatAttachments(request.Attachments, agentValue.AgentID)
	runtimeContent, err := s.renderRuntimeContentWithAttachments(ctx, content, attachments)
	if err != nil {
		return false, err
	}
	// 轮内引导注入不带 runtime context（情绪态）：避免逐步污染 prompt 前缀缓存。
	runningRoundIDs, err := s.runtime.QueueGuidanceInput(ctx, sessionKey, request.RoundID, runtimeContent.PlainText())
	if err != nil {
		return false, err
	}
	s.broadcastEventWithTimeout(ctx, sessionKey, protocol.NewChatAckEvent(
		sessionKey,
		request.ClientRequestID,
		request.ClientMessageID,
		request.RoundID,
		request.UserMessageID,
		nil,
	))
	if request.BroadcastUserMessage {
		for _, targetRoundID := range runningRoundIDs {
			s.broadcastGuidanceMessage(ctx, sessionItem, targetRoundID, request.RoundID, content)
		}
	}
	s.broadcastSessionStatus(ctx, sessionKey)
	s.loggerFor(ctx).Info("登记 DM 引导消息等待 PostToolUse 注入",
		"session_key", sessionKey,
		"agent_id", agentValue.AgentID,
		"round_id", request.RoundID,
		"running_round_ids", runningRoundIDs,
		"content_chars", utf8.RuneCountInString(content),
		"content_preview", logx.PreviewText(content, 240),
	)
	return true, nil
}

// externalReplyTargetToProtocol 将 dm ExternalReplyTarget 转换为协议序列化版本。
func externalReplyTargetToProtocol(target *ExternalReplyTarget) *protocol.InputQueueReplyTarget {
	if target == nil {
		return nil
	}
	return &protocol.InputQueueReplyTarget{
		Mode:       target.Mode,
		Channel:    target.Channel,
		To:         target.To,
		AccountID:  target.AccountID,
		ThreadID:   target.ThreadID,
		SessionKey: target.SessionKey,
	}
}

// externalReplyTargetFromProtocol 将协议序列化版本转换回 dm ExternalReplyTarget。
func externalReplyTargetFromProtocol(target *protocol.InputQueueReplyTarget) *ExternalReplyTarget {
	if target == nil {
		return nil
	}
	return &ExternalReplyTarget{
		Mode:       target.Mode,
		Channel:    target.Channel,
		To:         target.To,
		AccountID:  target.AccountID,
		ThreadID:   target.ThreadID,
		SessionKey: target.SessionKey,
	}
}
