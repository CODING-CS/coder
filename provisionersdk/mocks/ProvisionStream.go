// Code generated by mockery v2.12.3. DO NOT EDIT.

package mocks

import (
	proto "github.com/coder/coder/provisionersdk/proto"
	mock "github.com/stretchr/testify/mock"
)

// ProvisionStream is an autogenerated mock type for the ProvisionStream type
type ProvisionStream struct {
	mock.Mock
}

// Send provides a mock function with given fields: _a0
func (_m *ProvisionStream) Send(_a0 *proto.Provision_Response) error {
	ret := _m.Called(_a0)

	var r0 error
	if rf, ok := ret.Get(0).(func(*proto.Provision_Response) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

type NewProvisionStreamT interface {
	mock.TestingT
	Cleanup(func())
}

// NewProvisionStream creates a new instance of ProvisionStream. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewProvisionStream(t NewProvisionStreamT) *ProvisionStream {
	mock := &ProvisionStream{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
