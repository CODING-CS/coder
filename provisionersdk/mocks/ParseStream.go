// Code generated by mockery v2.12.3. DO NOT EDIT.

package mocks

import (
	proto "github.com/coder/coder/provisionersdk/proto"
	mock "github.com/stretchr/testify/mock"
)

// ParseStream is an autogenerated mock type for the ParseStream type
type ParseStream struct {
	mock.Mock
}

// Send provides a mock function with given fields: response
func (_m *ParseStream) Send(response *proto.Parse_Response) error {
	ret := _m.Called(response)

	var r0 error
	if rf, ok := ret.Get(0).(func(*proto.Parse_Response) error); ok {
		r0 = rf(response)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

type NewParseStreamT interface {
	mock.TestingT
	Cleanup(func())
}

// NewParseStream creates a new instance of ParseStream. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewParseStream(t NewParseStreamT) *ParseStream {
	mock := &ParseStream{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
