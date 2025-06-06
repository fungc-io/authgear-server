package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/authgear/authgear-server/pkg/api/apierrors"
	"github.com/authgear/authgear-server/pkg/api/event"
	"github.com/authgear/authgear-server/pkg/api/event/nonblocking"
	"github.com/authgear/authgear-server/pkg/lib/config"
	"github.com/authgear/authgear-server/pkg/lib/infra/db/appdb"
	"github.com/authgear/authgear-server/pkg/lib/infra/mail"
	"github.com/authgear/authgear-server/pkg/lib/infra/sms"
	"github.com/authgear/authgear-server/pkg/lib/infra/sms/smsapi"
	"github.com/authgear/authgear-server/pkg/lib/infra/whatsapp"
	"github.com/authgear/authgear-server/pkg/lib/otelauthgear"
	"github.com/authgear/authgear-server/pkg/lib/translation"
	"github.com/authgear/authgear-server/pkg/util/log"
	"github.com/authgear/authgear-server/pkg/util/phone"
)

type Logger struct{ *log.Logger }

func NewLogger(lf *log.Factory) Logger {
	return Logger{lf.New("messaging")}
}

type EventService interface {
	DispatchEventImmediately(ctx context.Context, payload event.NonBlockingPayload) error
}

type MailSender interface {
	Send(opts mail.SendOptions) error
}

type SMSSender interface {
	Send(ctx context.Context, client smsapi.Client, opts sms.SendOptions) error
	ResolveClient() (smsapi.Client, error)
}

type WhatsappSender interface {
	SendAuthenticationOTP(ctx context.Context, opts *whatsapp.SendAuthenticationOTPOptions) error
}

type Sender struct {
	Logger         Logger
	Limits         Limits
	Events         EventService
	MailSender     MailSender
	SMSSender      SMSSender
	WhatsappSender WhatsappSender
	Database       *appdb.Handle

	DevMode config.DevMode

	MessagingFeatureConfig *config.MessagingFeatureConfig

	FeatureTestModeEmailSuppressed config.FeatureTestModeEmailSuppressed
	TestModeEmailConfig            *config.TestModeEmailConfig

	FeatureTestModeSMSSuppressed config.FeatureTestModeSMSSuppressed
	TestModeSMSConfig            *config.TestModeSMSConfig

	FeatureTestModeWhatsappSuppressed config.FeatureTestModeWhatsappSuppressed
	TestModeWhatsappConfig            *config.TestModeWhatsappConfig
}

func (s *Sender) SendEmailInNewGoroutine(ctx context.Context, msgType translation.MessageType, opts *mail.SendOptions) error {
	err := s.Limits.checkEmail(ctx, opts.Recipient)
	if err != nil {
		return err
	}

	if s.FeatureTestModeEmailSuppressed {
		return s.testModeSendEmail(ctx, msgType, opts)
	}

	if s.TestModeEmailConfig.Enabled {
		if r, ok := s.TestModeEmailConfig.MatchTarget(opts.Recipient); ok && r.Suppressed {
			return s.testModeSendEmail(ctx, msgType, opts)
		}
	}

	if s.DevMode {
		return s.devModeSendEmail(ctx, msgType, opts)
	}

	sendInTx := func(ctx context.Context) error {
		err := s.MailSender.Send(*opts)
		if err != nil {
			otelauthgear.IntCounterAddOne(
				ctx,
				otelauthgear.CounterEmailRequestCount,
				otelauthgear.WithStatusError(),
			)
			dispatchErr := s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.EmailErrorEventPayload{
				Description: s.errorToDescription(err),
			})
			if dispatchErr != nil {
				s.Logger.WithError(dispatchErr).Errorf("failed to emit %v event", nonblocking.EmailError)
			}
			return err
		}

		otelauthgear.IntCounterAddOne(
			ctx,
			otelauthgear.CounterEmailRequestCount,
			otelauthgear.WithStatusOk(),
		)

		dispatchErr := s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.EmailSentEventPayload{
			Sender:    opts.Sender,
			Recipient: opts.Recipient,
			Type:      string(msgType),
		})
		if dispatchErr != nil {
			s.Logger.WithError(dispatchErr).Errorf("failed to emit %v event", nonblocking.EmailSent)
		}
		return nil
	}

	// Detach the deadline so that the context is not canceled along with the request.
	ctxWithoutCancel := context.WithoutCancel(ctx)
	go func(ctx context.Context) {
		// Always use a new transaction to send in async routine
		asyncErr := s.Database.ReadOnly(ctx, func(ctx context.Context) error {
			return sendInTx(ctx)
		})
		if asyncErr != nil {
			s.Logger.WithError(asyncErr).WithFields(logrus.Fields{
				"email": mail.MaskAddress(opts.Recipient),
			}).Error("failed to send email")
		}
	}(ctxWithoutCancel)

	return nil
}

func (s *Sender) testModeSendEmail(ctx context.Context, msgType translation.MessageType, opts *mail.SendOptions) error {
	s.Logger.
		WithField("message_type", string(msgType)).
		WithField("recipient", opts.Recipient).
		WithField("body", opts.TextBody).
		WithField("sender", opts.Sender).
		WithField("subject", opts.Subject).
		WithField("reply_to", opts.ReplyTo).
		Warn("email is suppressed by test mode")

	desc := fmt.Sprintf("email (%v) to %v is suppressed by test mode.", msgType, opts.Recipient)
	return s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.EmailSuppressedEventPayload{
		Description: desc,
	})
}

func (s *Sender) devModeSendEmail(ctx context.Context, msgType translation.MessageType, opts *mail.SendOptions) error {
	s.Logger.
		WithField("message_type", string(msgType)).
		WithField("recipient", opts.Recipient).
		WithField("body", opts.TextBody).
		WithField("sender", opts.Sender).
		WithField("subject", opts.Subject).
		WithField("reply_to", opts.ReplyTo).
		Warn("email is suppressed by development mode")

	desc := fmt.Sprintf("email (%v) to %v is suppressed by development mode", msgType, opts.Recipient)
	return s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.EmailSuppressedEventPayload{
		Description: desc,
	})
}

func (s *Sender) SendSMSInNewGoroutine(ctx context.Context, msgType translation.MessageType, opts *sms.SendOptions) error {
	return s.sendSMS(ctx, msgType, opts, true)
}

func (s *Sender) SendSMSImmediately(ctx context.Context, msgType translation.MessageType, opts *sms.SendOptions) error {
	return s.sendSMS(ctx, msgType, opts, false)
}

func (s *Sender) sendSMS(ctx context.Context, msgType translation.MessageType, opts *sms.SendOptions, isAsync bool) error {
	err := s.Limits.checkSMS(ctx, opts.To)
	if err != nil {
		return err
	}

	if s.FeatureTestModeSMSSuppressed {
		return s.testModeSendSMS(ctx, msgType, opts)
	}

	if s.TestModeSMSConfig.Enabled {
		if r, ok := s.TestModeSMSConfig.MatchTarget(opts.To); ok && r.Suppressed {
			return s.testModeSendSMS(ctx, msgType, opts)
		}
	}

	if s.DevMode {
		return s.devModeSendSMS(ctx, msgType, opts)
	}

	client, err := s.SMSSender.ResolveClient()
	if err != nil {
		return err
	}

	sendInTx := func(ctx context.Context) error {
		err = s.SMSSender.Send(ctx, client, *opts)
		if err != nil {
			otelauthgear.IntCounterAddOne(
				ctx,
				otelauthgear.CounterSMSRequestCount,
				otelauthgear.WithStatusError(),
			)
			dispatchErr := s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.SMSErrorEventPayload{
				Description: s.errorToDescription(err),
			})
			if dispatchErr != nil {
				s.Logger.WithError(dispatchErr).Errorf("failed to emit %v event", nonblocking.SMSError)
			}
			return err
		}
		return nil
	}

	if isAsync {
		// Detach the deadline so that the context is not canceled along with the request.
		ctxWithoutCancel := context.WithoutCancel(ctx)
		go func(ctx context.Context) {
			// Always use a new transaction to send in async routine
			asyncErr := s.Database.ReadOnly(ctx, func(ctx context.Context) error {
				return sendInTx(ctx)
			})
			if asyncErr != nil {
				// TODO: Handle expected errors https://linear.app/authgear/issue/DEV-1139
				s.Logger.WithError(asyncErr).WithFields(logrus.Fields{
					"phone": phone.Mask(opts.To),
				}).Error("failed to send SMS")
			}
		}(ctxWithoutCancel)
	} else {
		err = sendInTx(ctx)
		if err != nil {
			return err
		}
	}

	otelauthgear.IntCounterAddOne(
		ctx,
		otelauthgear.CounterSMSRequestCount,
		otelauthgear.WithStatusOk(),
	)

	dispatchErr := s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.SMSSentEventPayload{
		Sender:              opts.Sender,
		Recipient:           opts.To,
		Type:                string(msgType),
		IsNotCountedInUsage: *s.MessagingFeatureConfig.SMSUsageCountDisabled,
	})
	if dispatchErr != nil {
		s.Logger.WithError(dispatchErr).Errorf("failed to emit %v event", nonblocking.SMSSent)
	}

	return nil
}

func (s *Sender) testModeSendSMS(ctx context.Context, msgType translation.MessageType, opts *sms.SendOptions) error {
	s.Logger.
		WithField("message_type", string(msgType)).
		WithField("recipient", opts.To).
		WithField("sender", opts.Sender).
		WithField("body", opts.Body).
		WithField("app_id", opts.AppID).
		WithField("template_name", opts.TemplateName).
		WithField("language_tag", opts.LanguageTag).
		WithField("template_variables", opts.TemplateVariables).
		Warn("SMS is suppressed in test mode")

	desc := fmt.Sprintf("SMS (%v) to %v is suppressed by test mode.", msgType, opts.To)
	return s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.SMSSuppressedEventPayload{
		Description: desc,
	})
}

func (s *Sender) devModeSendSMS(ctx context.Context, msgType translation.MessageType, opts *sms.SendOptions) error {
	s.Logger.
		WithField("message_type", string(msgType)).
		WithField("recipient", opts.To).
		WithField("sender", opts.Sender).
		WithField("body", opts.Body).
		WithField("app_id", opts.AppID).
		WithField("template_name", opts.TemplateName).
		WithField("language_tag", opts.LanguageTag).
		WithField("template_variables", opts.TemplateVariables).
		Warn("SMS is suppressed in development mode")

	desc := fmt.Sprintf("SMS (%v) to %v is suppressed by development mode.", msgType, opts.To)
	return s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.SMSSuppressedEventPayload{
		Description: desc,
	})
}

func (s *Sender) SendWhatsappImmediately(ctx context.Context, msgType translation.MessageType, opts *whatsapp.SendAuthenticationOTPOptions) error {
	err := s.Limits.checkWhatsapp(ctx, opts.To)
	if err != nil {
		return err
	}

	if s.FeatureTestModeWhatsappSuppressed {
		return s.testModeSendWhatsapp(ctx, msgType, opts)
	}

	if s.TestModeWhatsappConfig.Enabled {
		if r, ok := s.TestModeWhatsappConfig.MatchTarget(opts.To); ok && r.Suppressed {
			return s.testModeSendWhatsapp(ctx, msgType, opts)
		}
	}

	if s.DevMode {
		return s.devModeSendWhatsapp(ctx, msgType, opts)
	}

	// Send immediately.
	err = s.sendWhatsapp(ctx, opts)
	if err != nil {

		metricOptions := []otelauthgear.MetricOption{otelauthgear.WithStatusError()}
		var apiErr *whatsapp.WhatsappAPIError
		if ok := errors.As(err, &apiErr); ok {
			metricOptions = append(metricOptions, otelauthgear.WithWhatsappAPIType(apiErr.APIType))
			metricOptions = append(metricOptions, otelauthgear.WithHTTPStatusCode(apiErr.HTTPStatusCode))
			errorCode, ok := apiErr.GetErrorCode()
			if ok {
				metricOptions = append(metricOptions, otelauthgear.WithWhatsappAPIErrorCode(errorCode))
			}
		}

		otelauthgear.IntCounterAddOne(
			ctx,
			otelauthgear.CounterWhatsappRequestCount,
			metricOptions...,
		)

		s.Logger.WithError(err).WithFields(logrus.Fields{
			"phone": phone.Mask(opts.To),
		}).Error("failed to send Whatsapp")

		dispatchErr := s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.WhatsappErrorEventPayload{
			Description: s.errorToDescription(err),
		})
		if dispatchErr != nil {
			s.Logger.WithError(dispatchErr).Errorf("failed to emit %v event", nonblocking.WhatsappError)
		}

		return err
	}

	otelauthgear.IntCounterAddOne(
		ctx,
		otelauthgear.CounterWhatsappRequestCount,
		otelauthgear.WithStatusOk(),
	)

	dispatchErr := s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.WhatsappSentEventPayload{
		Recipient:           opts.To,
		Type:                string(msgType),
		IsNotCountedInUsage: *s.MessagingFeatureConfig.WhatsappUsageCountDisabled,
	})
	if dispatchErr != nil {
		s.Logger.WithError(dispatchErr).Errorf("failed to emit %v event", nonblocking.WhatsappSent)
	}

	return nil
}

func (s *Sender) sendWhatsapp(ctx context.Context, opts *whatsapp.SendAuthenticationOTPOptions) error {
	err := s.WhatsappSender.SendAuthenticationOTP(ctx, opts)
	if err != nil {
		return err
	}

	return nil
}

func (s *Sender) testModeSendWhatsapp(ctx context.Context, msgType translation.MessageType, opts *whatsapp.SendAuthenticationOTPOptions) error {
	entry := s.Logger.
		WithField("message_type", string(msgType)).
		WithField("recipient", opts.To).
		WithField("otp", opts.OTP)

	entry.Warn("Whatsapp is suppressed in test mode")
	desc := fmt.Sprintf("Whatsapp (%v) to %v is suppressed by test mode.", msgType, opts.To)
	return s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.WhatsappSuppressedEventPayload{
		Description: desc,
	})
}

func (s *Sender) devModeSendWhatsapp(ctx context.Context, msgType translation.MessageType, opts *whatsapp.SendAuthenticationOTPOptions) error {
	entry := s.Logger.
		WithField("message_type", string(msgType)).
		WithField("recipient", opts.To).
		WithField("otp", opts.OTP)

	entry.Warn("Whatsapp is suppressed in development mode")
	desc := fmt.Sprintf("Whatsapp (%v) to %v is suppressed by development mode.", msgType, opts.To)
	return s.DispatchEventImmediatelyWithTx(ctx, &nonblocking.WhatsappSuppressedEventPayload{
		Description: desc,
	})
}

func (s *Sender) DispatchEventImmediatelyWithTx(ctx context.Context, payload event.NonBlockingPayload) error {
	if s.Database.IsInTx(ctx) {
		return s.Events.DispatchEventImmediately(ctx, payload)
	}

	return s.Database.ReadOnly(ctx, func(ctx context.Context) error {
		return s.Events.DispatchEventImmediately(ctx, payload)
	})
}

func (s *Sender) errorToDescription(err error) string {
	// APIError.Error() shows message only, but we want to show the full content of it.
	// Modifying APIError.Error is another big change that I do not want to deal with here.
	if apierrors.IsAPIError(err) {
		apiError := apierrors.AsAPIError(err)
		b, err := json.Marshal(apiError)
		if err != nil {
			panic(err)
		}
		return string(b)
	}

	return err.Error()
}
