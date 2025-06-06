package idpsession

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/authgear/authgear-server/pkg/lib/config"
	"github.com/authgear/authgear-server/pkg/lib/infra/redis/appredis"
	"github.com/authgear/authgear-server/pkg/lib/session"
	"github.com/authgear/authgear-server/pkg/lib/session/access"
	"github.com/authgear/authgear-server/pkg/util/clock"
	"github.com/authgear/authgear-server/pkg/util/crypto"
	"github.com/authgear/authgear-server/pkg/util/httputil"
	corerand "github.com/authgear/authgear-server/pkg/util/rand"
	"github.com/authgear/authgear-server/pkg/util/uuid"
)

//go:generate go tool mockgen -source=provider.go -destination=provider_mock_test.go -package idpsession

const (
	tokenAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	tokenLength   = 32
)

type AccessEventProvider interface {
	InitStream(ctx context.Context, sessionID string, expiry time.Time, event *access.Event) error
	RecordAccess(ctx context.Context, sessionID string, expiry time.Time, event *access.Event) error
}

type ProviderMeterService interface {
	TrackActiveUser(ctx context.Context, userID string) error
}

type Rand *rand.Rand

type Provider struct {
	RemoteIP        httputil.RemoteIP
	UserAgentString httputil.UserAgentString
	AppID           config.AppID
	Redis           *appredis.Handle
	Store           Store
	AccessEvents    AccessEventProvider
	MeterService    ProviderMeterService
	TrustProxy      config.TrustProxy
	Config          *config.SessionConfig
	Clock           clock.Clock
	Random          Rand
}

func (p *Provider) MakeSession(attrs *session.Attrs) (*IDPSession, string) {
	now := p.Clock.NowUTC()
	accessEvent := access.NewEvent(now, p.RemoteIP, p.UserAgentString)
	session := &IDPSession{
		ID:              uuid.New(),
		CreatedAt:       now,
		AuthenticatedAt: now,
		Attrs:           *attrs,
		AccessInfo: access.Info{
			InitialAccess: accessEvent,
			LastAccess:    accessEvent,
		},
	}
	setSessionExpireAtForResolvedSession(session, p.Config)
	token := p.generateToken(session)

	return session, token
}

func (p *Provider) Reauthenticate(ctx context.Context, id string, amr []string) (err error) {
	mutexName := sessionMutexName(p.AppID, id)
	mutex := p.Redis.NewMutex(mutexName)
	err = mutex.LockContext(ctx)
	if err != nil {
		return
	}
	defer func() {
		_, _ = mutex.UnlockContext(ctx)
	}()

	s, err := p.Get(ctx, id)
	if err != nil {
		return
	}

	now := p.Clock.NowUTC()
	s.AuthenticatedAt = now
	s.Attrs.SetAMR(amr)

	setSessionExpireAtForResolvedSession(s, p.Config)
	err = p.Store.Update(ctx, s, s.ExpireAtForResolvedSession)
	if err != nil {
		err = fmt.Errorf("failed to update session: %w", err)
		return err
	}

	return nil
}

func (p *Provider) Create(ctx context.Context, session *IDPSession) error {
	setSessionExpireAtForResolvedSession(session, p.Config)
	err := p.Store.Create(ctx, session, session.ExpireAtForResolvedSession)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	err = p.AccessEvents.InitStream(ctx, session.ID, session.ExpireAtForResolvedSession, &session.AccessInfo.InitialAccess)
	if err != nil {
		return fmt.Errorf("failed to access session: %w", err)
	}

	return nil
}

func (p *Provider) GetByToken(ctx context.Context, token string) (*IDPSession, error) {
	id, ok := decodeTokenSessionID(token)
	if !ok {
		return nil, ErrSessionNotFound
	}

	s, err := p.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if s.TokenHash == "" {
		return nil, ErrSessionNotFound
	}

	if !matchTokenHash(s.TokenHash, token) {
		return nil, ErrSessionNotFound
	}

	if p.CheckSessionExpired(s) {
		return nil, ErrSessionNotFound
	}

	return s, nil
}

func (p *Provider) Get(ctx context.Context, id string) (*IDPSession, error) {
	session, err := p.Store.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrSessionNotFound) {
			err = fmt.Errorf("failed to get session: %w", err)
		}
		return nil, err
	}
	setSessionExpireAtForResolvedSession(session, p.Config)

	return session, nil
}

func (p *Provider) AccessWithToken(ctx context.Context, token string, accessEvent access.Event) (*IDPSession, error) {
	s, err := p.GetByToken(ctx, token)
	if err != nil {
		return nil, err
	}

	ss, err := p.accessWithID(ctx, s.ID, accessEvent)

	return ss, err
}

func (p *Provider) AccessWithID(ctx context.Context, id string, accessEvent access.Event) (*IDPSession, error) {
	return p.accessWithID(ctx, id, accessEvent)
}

func (p *Provider) accessWithID(ctx context.Context, id string, accessEvent access.Event) (s *IDPSession, err error) {
	mutexName := sessionMutexName(p.AppID, id)
	mutex := p.Redis.NewMutex(mutexName)
	err = mutex.LockContext(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = mutex.UnlockContext(ctx)
	}()

	s, err = p.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	s.AccessInfo.LastAccess = accessEvent
	defer func() {
		if err == nil && s != nil {
			err = p.accessSideEffects(ctx, s, accessEvent)
		}
	}()

	setSessionExpireAtForResolvedSession(s, p.Config)

	err = p.Store.Update(ctx, s, s.ExpireAtForResolvedSession)
	if err != nil {
		err = fmt.Errorf("failed to update session: %w", err)
		return nil, err
	}

	return s, nil
}

func (p *Provider) accessSideEffects(ctx context.Context, session *IDPSession, accessEvent access.Event) error {

	err := p.AccessEvents.RecordAccess(ctx, session.SessionID(), session.GetExpireAt(), &accessEvent)
	if err != nil {
		return err
	}

	err = p.MeterService.TrackActiveUser(ctx, session.GetAuthenticationInfo().UserID)
	if err != nil {
		return err
	}

	return nil
}

func (p *Provider) AddSAMLServiceProviderParticipant(ctx context.Context, session *IDPSession, serviceProviderID string) (*IDPSession, error) {
	mutexName := sessionMutexName(p.AppID, session.ID)
	mutex := p.Redis.NewMutex(mutexName)
	err := mutex.LockContext(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = mutex.UnlockContext(ctx)
	}()

	s, err := p.Get(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	newParticipatedSAMLServiceProviderIDs := s.GetParticipatedSAMLServiceProviderIDsSet()
	newParticipatedSAMLServiceProviderIDs.Add(serviceProviderID)
	s.ParticipatedSAMLServiceProviderIDs = newParticipatedSAMLServiceProviderIDs.Keys()
	err = p.Store.Update(ctx, s, s.ExpireAtForResolvedSession)
	if err != nil {
		err = fmt.Errorf("failed to update session: %w", err)
		return nil, err
	}

	return s, nil
}

func (p *Provider) CheckSessionExpired(session *IDPSession) (expired bool) {
	now := p.Clock.NowUTC()
	cloned := *session
	setSessionExpireAtForResolvedSession(&cloned, p.Config)
	if now.After(cloned.ExpireAtForResolvedSession) {
		expired = true
	}

	return
}

func (p *Provider) generateToken(s *IDPSession) string {
	token := encodeToken(s.ID, corerand.StringWithAlphabet(tokenLength, tokenAlphabet, p.Random))
	s.TokenHash = hashToken(token)
	return token
}

func matchTokenHash(expectedHash, inputToken string) bool {
	inputHash := hashToken(inputToken)
	return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(inputHash)) == 1
}

func encodeToken(id string, token string) string {
	return fmt.Sprintf("%s.%s", id, token)
}

func hashToken(token string) string {
	return crypto.SHA256String(token)
}

func decodeTokenSessionID(token string) (id string, ok bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return
	}
	id, ok = parts[0], true
	return
}

func sessionMutexName(appID config.AppID, sessionID string) string {
	return fmt.Sprintf("app:%s:session-mutex:%s", appID, sessionID)
}
