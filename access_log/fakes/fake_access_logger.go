// This file was generated by counterfeiter
package fakes

import (
	"sync"

	"github.com/cloudfoundry/gorouter/access_log"
)

type FakeAccessLogger struct {
	RunStub         func()
	runMutex        sync.RWMutex
	runArgsForCall  []struct{}
	StopStub        func()
	stopMutex       sync.RWMutex
	stopArgsForCall []struct{}
	LogStub         func(record access_log.AccessLogRecord)
	logMutex        sync.RWMutex
	logArgsForCall  []struct {
		record access_log.AccessLogRecord
	}
}

func (fake *FakeAccessLogger) Run() {
	fake.runMutex.Lock()
	fake.runArgsForCall = append(fake.runArgsForCall, struct{}{})
	fake.runMutex.Unlock()
	if fake.RunStub != nil {
		fake.RunStub()
	}
}

func (fake *FakeAccessLogger) RunCallCount() int {
	fake.runMutex.RLock()
	defer fake.runMutex.RUnlock()
	return len(fake.runArgsForCall)
}

func (fake *FakeAccessLogger) Stop() {
	fake.stopMutex.Lock()
	fake.stopArgsForCall = append(fake.stopArgsForCall, struct{}{})
	fake.stopMutex.Unlock()
	if fake.StopStub != nil {
		fake.StopStub()
	}
}

func (fake *FakeAccessLogger) StopCallCount() int {
	fake.stopMutex.RLock()
	defer fake.stopMutex.RUnlock()
	return len(fake.stopArgsForCall)
}

func (fake *FakeAccessLogger) Log(record access_log.AccessLogRecord) {
	fake.logMutex.Lock()
	fake.logArgsForCall = append(fake.logArgsForCall, struct {
		record access_log.AccessLogRecord
	}{record})
	fake.logMutex.Unlock()
	if fake.LogStub != nil {
		fake.LogStub(record)
	}
}

func (fake *FakeAccessLogger) LogCallCount() int {
	fake.logMutex.RLock()
	defer fake.logMutex.RUnlock()
	return len(fake.logArgsForCall)
}

func (fake *FakeAccessLogger) LogArgsForCall(i int) access_log.AccessLogRecord {
	fake.logMutex.RLock()
	defer fake.logMutex.RUnlock()
	return fake.logArgsForCall[i].record
}

var _ access_log.AccessLogger = new(FakeAccessLogger)
