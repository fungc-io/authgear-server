package samlprotocol

import (
	"github.com/beevik/etree"
)

type SAMLErrorCode string

const (
	SAMLErrorCodeServiceProviderNotFound SAMLErrorCode = "service_provider_not_found"
	SAMLErrorCodeInvalidRequest          SAMLErrorCode = "invalid_request"
	SAMLErrorCodeInvalidSignature        SAMLErrorCode = "invalid_signature"
	SAMLErrorCodeParseRequestFailed      SAMLErrorCode = "parse_request_failed"
	SAMLErrorCodeMissingNameID           SAMLErrorCode = "missing_nameid"
	SAMLUnsupportedAttributeType         SAMLErrorCode = "unsupported_attribute_type"
)

// This error can be thrown in any code related to SAML, mainly in saml.Service
type SAMLErrorCodeError interface {
	error
	ErrorCode() SAMLErrorCode
	GetDetailElements() []*etree.Element
}
