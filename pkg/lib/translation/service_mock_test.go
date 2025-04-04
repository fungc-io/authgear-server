// Code generated by MockGen. DO NOT EDIT.
// Source: service.go

// Package translation_test is a generated GoMock package.
package translation_test

import (
	context "context"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
)

// MockStaticAssetResolver is a mock of StaticAssetResolver interface.
type MockStaticAssetResolver struct {
	ctrl     *gomock.Controller
	recorder *MockStaticAssetResolverMockRecorder
}

// MockStaticAssetResolverMockRecorder is the mock recorder for MockStaticAssetResolver.
type MockStaticAssetResolverMockRecorder struct {
	mock *MockStaticAssetResolver
}

// NewMockStaticAssetResolver creates a new mock instance.
func NewMockStaticAssetResolver(ctrl *gomock.Controller) *MockStaticAssetResolver {
	mock := &MockStaticAssetResolver{ctrl: ctrl}
	mock.recorder = &MockStaticAssetResolverMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockStaticAssetResolver) EXPECT() *MockStaticAssetResolverMockRecorder {
	return m.recorder
}

// StaticAssetURL mocks base method.
func (m *MockStaticAssetResolver) StaticAssetURL(ctx context.Context, id string) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "StaticAssetURL", ctx, id)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// StaticAssetURL indicates an expected call of StaticAssetURL.
func (mr *MockStaticAssetResolverMockRecorder) StaticAssetURL(ctx, id interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "StaticAssetURL", reflect.TypeOf((*MockStaticAssetResolver)(nil).StaticAssetURL), ctx, id)
}
