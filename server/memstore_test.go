// Copyright 2019-2023 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestMemStoreBasics(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}

	subj, msg := "foo", []byte("Hello World")
	now := time.Now().UnixNano()
	if seq, ts, err := ms.StoreMsg(subj, nil, msg); err != nil {
		t.Fatalf("Error storing msg: %v", err)
	} else if seq != 1 {
		t.Fatalf("Expected sequence to be 1, got %d", seq)
	} else if ts < now || ts > now+int64(time.Millisecond) {
		t.Fatalf("Expected timestamp to be current, got %v", ts-now)
	}

	state := ms.State()
	if state.Msgs != 1 {
		t.Fatalf("Expected 1 msg, got %d", state.Msgs)
	}
	expectedSize := memStoreMsgSize(subj, nil, msg)
	if state.Bytes != expectedSize {
		t.Fatalf("Expected %d bytes, got %d", expectedSize, state.Bytes)
	}
	sm, err := ms.LoadMsg(1, nil)
	if err != nil {
		t.Fatalf("Unexpected error looking up msg: %v", err)
	}
	if sm.subj != subj {
		t.Fatalf("Subjects don't match, original %q vs %q", subj, sm.subj)
	}
	if !bytes.Equal(sm.msg, msg) {
		t.Fatalf("Msgs don't match, original %q vs %q", msg, sm.msg)
	}
}

func TestMemStoreMsgLimit(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage, MaxMsgs: 10})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}
	subj, msg := "foo", []byte("Hello World")
	for i := 0; i < 10; i++ {
		ms.StoreMsg(subj, nil, msg)
	}
	state := ms.State()
	if state.Msgs != 10 {
		t.Fatalf("Expected %d msgs, got %d", 10, state.Msgs)
	}
	if _, _, err := ms.StoreMsg(subj, nil, msg); err != nil {
		t.Fatalf("Error storing msg: %v", err)
	}
	state = ms.State()
	if state.Msgs != 10 {
		t.Fatalf("Expected %d msgs, got %d", 10, state.Msgs)
	}
	if state.LastSeq != 11 {
		t.Fatalf("Expected the last sequence to be 11 now, but got %d", state.LastSeq)
	}
	if state.FirstSeq != 2 {
		t.Fatalf("Expected the first sequence to be 2 now, but got %d", state.FirstSeq)
	}
	// Make sure we can not lookup seq 1.
	if _, err := ms.LoadMsg(1, nil); err == nil {
		t.Fatalf("Expected error looking up seq 1 but got none")
	}
}

func TestMemStoreBytesLimit(t *testing.T) {
	subj, msg := "foo", make([]byte, 512)
	storedMsgSize := memStoreMsgSize(subj, nil, msg)

	toStore := uint64(1024)
	maxBytes := storedMsgSize * toStore

	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage, MaxBytes: int64(maxBytes)})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}

	for i := uint64(0); i < toStore; i++ {
		ms.StoreMsg(subj, nil, msg)
	}
	state := ms.State()
	if state.Msgs != toStore {
		t.Fatalf("Expected %d msgs, got %d", toStore, state.Msgs)
	}
	if state.Bytes != storedMsgSize*toStore {
		t.Fatalf("Expected bytes to be %d, got %d", storedMsgSize*toStore, state.Bytes)
	}

	// Now send 10 more and check that bytes limit enforced.
	for i := 0; i < 10; i++ {
		if _, _, err := ms.StoreMsg(subj, nil, msg); err != nil {
			t.Fatalf("Error storing msg: %v", err)
		}
	}
	state = ms.State()
	if state.Msgs != toStore {
		t.Fatalf("Expected %d msgs, got %d", toStore, state.Msgs)
	}
	if state.Bytes != storedMsgSize*toStore {
		t.Fatalf("Expected bytes to be %d, got %d", storedMsgSize*toStore, state.Bytes)
	}
	if state.FirstSeq != 11 {
		t.Fatalf("Expected first sequence to be 11, got %d", state.FirstSeq)
	}
	if state.LastSeq != toStore+10 {
		t.Fatalf("Expected last sequence to be %d, got %d", toStore+10, state.LastSeq)
	}
}

func TestMemStoreAgeLimit(t *testing.T) {
	maxAge := 10 * time.Millisecond
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage, MaxAge: maxAge})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}
	// Store some messages. Does not really matter how many.
	subj, msg := "foo", []byte("Hello World")
	toStore := 100
	for i := 0; i < toStore; i++ {
		ms.StoreMsg(subj, nil, msg)
	}
	state := ms.State()
	if state.Msgs != uint64(toStore) {
		t.Fatalf("Expected %d msgs, got %d", toStore, state.Msgs)
	}
	checkExpired := func(t *testing.T) {
		t.Helper()
		checkFor(t, time.Second, maxAge, func() error {
			state = ms.State()
			if state.Msgs != 0 {
				return fmt.Errorf("Expected no msgs, got %d", state.Msgs)
			}
			if state.Bytes != 0 {
				return fmt.Errorf("Expected no bytes, got %d", state.Bytes)
			}
			return nil
		})
	}
	// Let them expire
	checkExpired(t)
	// Now add some more and make sure that timer will fire again.
	for i := 0; i < toStore; i++ {
		ms.StoreMsg(subj, nil, msg)
	}
	state = ms.State()
	if state.Msgs != uint64(toStore) {
		t.Fatalf("Expected %d msgs, got %d", toStore, state.Msgs)
	}
	checkExpired(t)
}

func TestMemStoreTimeStamps(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}
	last := time.Now().UnixNano()
	subj, msg := "foo", []byte("Hello World")
	for i := 0; i < 10; i++ {
		time.Sleep(5 * time.Microsecond)
		ms.StoreMsg(subj, nil, msg)
	}
	var smv StoreMsg
	for seq := uint64(1); seq <= 10; seq++ {
		sm, err := ms.LoadMsg(seq, &smv)
		if err != nil {
			t.Fatalf("Unexpected error looking up msg: %v", err)
		}
		// These should be different
		if sm.ts <= last {
			t.Fatalf("Expected different timestamps, got %v", sm.ts)
		}
		last = sm.ts
	}
}

func TestMemStorePurge(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}

	subj, msg := "foo", []byte("Hello World")
	for i := 0; i < 10; i++ {
		ms.StoreMsg(subj, nil, msg)
	}
	if state := ms.State(); state.Msgs != 10 {
		t.Fatalf("Expected 10 msgs, got %d", state.Msgs)
	}
	ms.Purge()
	if state := ms.State(); state.Msgs != 0 {
		t.Fatalf("Expected no msgs, got %d", state.Msgs)
	}
}

func TestMemStoreCompact(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}

	subj, msg := "foo", []byte("Hello World")
	for i := 0; i < 10; i++ {
		ms.StoreMsg(subj, nil, msg)
	}
	if state := ms.State(); state.Msgs != 10 {
		t.Fatalf("Expected 10 msgs, got %d", state.Msgs)
	}
	n, err := ms.Compact(6)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("Expected to have purged 5 msgs, got %d", n)
	}
	state := ms.State()
	if state.Msgs != 5 {
		t.Fatalf("Expected 5 msgs, got %d", state.Msgs)
	}
	if state.FirstSeq != 6 {
		t.Fatalf("Expected first seq of 6, got %d", state.FirstSeq)
	}
	// Now test that compact will also reset first if seq > last
	n, err = ms.Compact(100)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("Expected to have purged 5 msgs, got %d", n)
	}
	if state = ms.State(); state.FirstSeq != 100 {
		t.Fatalf("Expected first seq of 100, got %d", state.FirstSeq)
	}
}

func TestMemStoreEraseMsg(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}
	subj, msg := "foo", []byte("Hello World")
	ms.StoreMsg(subj, nil, msg)
	sm, err := ms.LoadMsg(1, nil)
	if err != nil {
		t.Fatalf("Unexpected error looking up msg: %v", err)
	}
	if !bytes.Equal(msg, sm.msg) {
		t.Fatalf("Expected same msg, got %q vs %q", sm.msg, msg)
	}
	if removed, _ := ms.EraseMsg(1); !removed {
		t.Fatalf("Expected erase msg to return success")
	}
}

func TestMemStoreMsgHeaders(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}
	subj, hdr, msg := "foo", []byte("name:derek"), []byte("Hello World")
	if sz := int(memStoreMsgSize(subj, hdr, msg)); sz != (len(subj) + len(hdr) + len(msg) + 16) {
		t.Fatalf("Wrong size for stored msg with header")
	}
	ms.StoreMsg(subj, hdr, msg)
	sm, err := ms.LoadMsg(1, nil)
	if err != nil {
		t.Fatalf("Unexpected error looking up msg: %v", err)
	}
	if !bytes.Equal(msg, sm.msg) {
		t.Fatalf("Expected same msg, got %q vs %q", sm.msg, msg)
	}
	if !bytes.Equal(hdr, sm.hdr) {
		t.Fatalf("Expected same hdr, got %q vs %q", sm.hdr, hdr)
	}
	if removed, _ := ms.EraseMsg(1); !removed {
		t.Fatalf("Expected erase msg to return success")
	}
}

func TestMemStoreStreamStateDeleted(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}

	subj, toStore := "foo", uint64(10)
	for i := uint64(1); i <= toStore; i++ {
		msg := []byte(fmt.Sprintf("[%08d] Hello World!", i))
		if _, _, err := ms.StoreMsg(subj, nil, msg); err != nil {
			t.Fatalf("Error storing msg: %v", err)
		}
	}
	state := ms.State()
	if len(state.Deleted) != 0 {
		t.Fatalf("Expected deleted to be empty")
	}
	// Now remove some interior messages.
	var expected []uint64
	for seq := uint64(2); seq < toStore; seq += 2 {
		ms.RemoveMsg(seq)
		expected = append(expected, seq)
	}
	state = ms.State()
	if !reflect.DeepEqual(state.Deleted, expected) {
		t.Fatalf("Expected deleted to be %+v, got %+v\n", expected, state.Deleted)
	}
	// Now fill the gap by deleting 1 and 3
	ms.RemoveMsg(1)
	ms.RemoveMsg(3)
	expected = expected[2:]
	state = ms.State()
	if !reflect.DeepEqual(state.Deleted, expected) {
		t.Fatalf("Expected deleted to be %+v, got %+v\n", expected, state.Deleted)
	}
	if state.FirstSeq != 5 {
		t.Fatalf("Expected first seq to be 5, got %d", state.FirstSeq)
	}
	ms.Purge()
	if state = ms.State(); len(state.Deleted) != 0 {
		t.Fatalf("Expected no deleted after purge, got %+v\n", state.Deleted)
	}
}

func TestMemStoreStreamTruncate(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage})
	if err != nil {
		t.Fatalf("Unexpected error creating store: %v", err)
	}

	tseq := uint64(50)

	subj, toStore := "foo", uint64(100)
	for i := uint64(1); i < tseq; i++ {
		_, _, err := ms.StoreMsg(subj, nil, []byte("ok"))
		require_NoError(t, err)
	}
	subj = "bar"
	for i := tseq; i <= toStore; i++ {
		_, _, err := ms.StoreMsg(subj, nil, []byte("ok"))
		require_NoError(t, err)
	}

	if state := ms.State(); state.Msgs != toStore {
		t.Fatalf("Expected %d msgs, got %d", toStore, state.Msgs)
	}

	// Check that sequence has to be interior.
	if err := ms.Truncate(toStore + 1); err != ErrInvalidSequence {
		t.Fatalf("Expected err of '%v', got '%v'", ErrInvalidSequence, err)
	}

	if err := ms.Truncate(tseq); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if state := ms.State(); state.Msgs != tseq {
		t.Fatalf("Expected %d msgs, got %d", tseq, state.Msgs)
	}

	// Now make sure we report properly if we have some deleted interior messages.
	ms.RemoveMsg(10)
	ms.RemoveMsg(20)
	ms.RemoveMsg(30)
	ms.RemoveMsg(40)

	tseq = uint64(25)
	if err := ms.Truncate(tseq); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	state := ms.State()
	if state.Msgs != tseq-2 {
		t.Fatalf("Expected %d msgs, got %d", tseq-2, state.Msgs)
	}
	if state.NumSubjects != 1 {
		t.Fatalf("Expected only 1 subject, got %d", state.NumSubjects)
	}
	expected := []uint64{10, 20}
	if !reflect.DeepEqual(state.Deleted, expected) {
		t.Fatalf("Expected deleted to be %+v, got %+v\n", expected, state.Deleted)
	}
}

func TestMemStorePurgeExWithSubject(t *testing.T) {
	ms, err := newMemStore(&StreamConfig{Storage: MemoryStorage})
	require_NoError(t, err)

	for i := 0; i < 100; i++ {
		_, _, err = ms.StoreMsg("foo", nil, nil)
		require_NoError(t, err)
	}

	// This should purge all.
	ms.PurgeEx("foo", 1, 0)
	require_True(t, ms.State().Msgs == 0)
}

func TestMemStoreUpdateMaxMsgsPerSubject(t *testing.T) {
	cfg := &StreamConfig{
		Name:       "TEST",
		Storage:    MemoryStorage,
		Subjects:   []string{"foo"},
		MaxMsgsPer: 10,
	}
	ms, err := newMemStore(cfg)
	require_NoError(t, err)

	// Make sure this is honored on an update.
	cfg.MaxMsgsPer = 50
	err = ms.UpdateConfig(cfg)
	require_NoError(t, err)

	numStored := 22
	for i := 0; i < numStored; i++ {
		_, _, err = ms.StoreMsg("foo", nil, nil)
		require_NoError(t, err)
	}

	ss := ms.SubjectsState("foo")["foo"]
	if ss.Msgs != uint64(numStored) {
		t.Fatalf("Expected to have %d stored, got %d", numStored, ss.Msgs)
	}

	// Now make sure we trunk if setting to lower value.
	cfg.MaxMsgsPer = 10
	err = ms.UpdateConfig(cfg)
	require_NoError(t, err)

	ss = ms.SubjectsState("foo")["foo"]
	if ss.Msgs != 10 {
		t.Fatalf("Expected to have %d stored, got %d", 10, ss.Msgs)
	}
}

func TestMemStoreStreamTruncateReset(t *testing.T) {
	cfg := &StreamConfig{
		Name:     "TEST",
		Storage:  MemoryStorage,
		Subjects: []string{"foo"},
	}
	ms, err := newMemStore(cfg)
	require_NoError(t, err)

	subj, msg := "foo", []byte("Hello World")
	for i := 0; i < 1000; i++ {
		_, _, err := ms.StoreMsg(subj, nil, msg)
		require_NoError(t, err)
	}

	// Reset everything
	require_NoError(t, ms.Truncate(0))

	state := ms.State()
	require_True(t, state.Msgs == 0)
	require_True(t, state.Bytes == 0)
	require_True(t, state.FirstSeq == 0)
	require_True(t, state.LastSeq == 0)
	require_True(t, state.NumSubjects == 0)
	require_True(t, state.NumDeleted == 0)

	for i := 0; i < 1000; i++ {
		_, _, err := ms.StoreMsg(subj, nil, msg)
		require_NoError(t, err)
	}

	state = ms.State()
	require_True(t, state.Msgs == 1000)
	require_True(t, state.Bytes == 30000)
	require_True(t, state.FirstSeq == 1)
	require_True(t, state.LastSeq == 1000)
	require_True(t, state.NumSubjects == 1)
	require_True(t, state.NumDeleted == 0)
}

func TestMemStoreStreamCompactMultiBlockSubjectInfo(t *testing.T) {
	cfg := &StreamConfig{
		Name:     "TEST",
		Storage:  MemoryStorage,
		Subjects: []string{"foo.*"},
	}
	ms, err := newMemStore(cfg)
	require_NoError(t, err)

	for i := 0; i < 1000; i++ {
		subj := fmt.Sprintf("foo.%d", i)
		_, _, err := ms.StoreMsg(subj, nil, []byte("Hello World"))
		require_NoError(t, err)
	}

	// Compact such that we know we throw blocks away from the beginning.
	deleted, err := ms.Compact(501)
	require_NoError(t, err)
	require_True(t, deleted == 500)

	// Make sure we adjusted for subjects etc.
	state := ms.State()
	require_True(t, state.NumSubjects == 500)
}
