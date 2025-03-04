// Copyright 2022-2023 The Parca Authors
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
//

package cpu

import (
	"syscall"
	"testing"
	"unsafe"

	"github.com/Masterminds/semver/v3"
	bpf "github.com/aquasecurity/libbpfgo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/parca-dev/parca-agent/pkg/kernel"
	"github.com/parca-dev/parca-agent/pkg/logger"
	bpfmaps "github.com/parca-dev/parca-agent/pkg/profiler/cpu/bpf/maps"
)

// bpfVerboseLoggingEnabled returns false if the verbose BPF logs should be disabled
// for the kernel versions.
func bpfVerboseLoggingEnabled() bool {
	kernelRelease, err := kernel.GetRelease()
	if err != nil {
		panic("bad kernel release")
	}
	constrain, err := semver.NewConstraint(">5.10")
	if err != nil {
		panic("bad constrain, this should never happen")
	}

	return constrain.Check(kernelRelease)
}

// The intent of these tests is to ensure that libbpfgo behaves the
// way we expect.
//
// We also use them to ensure that different kernel versions load our
// BPF program.
func SetUpBpfProgram(t *testing.T) (*bpf.Module, error) {
	t.Helper()
	logger := logger.NewLogger("debug", logger.LogFormatLogfmt, "parca-cpu-test")

	memLock := uint64(1200 * 1024 * 1024) // ~1.2GiB
	m, _, err := loadBPFModules(logger, prometheus.NewRegistry(), memLock, Config{
		DWARFUnwindingMixedModeEnabled: true,
		DWARFUnwindingDisabled:         false,
		BPFVerboseLoggingEnabled:       bpfVerboseLoggingEnabled(),
		BPFEventsBufferSize:            8192,
		PythonUnwindingEnabled:         false,
		RubyUnwindingEnabled:           false,
		RateLimitUnwindInfo:            50,
		RateLimitProcessMappings:       50,
		RateLimitRefreshProcessInfo:    50,
	})
	require.NoError(t, err)
	require.NotNil(t, m)

	return m, err
}

func TestDeleteNonExistentKeyReturnsEnoent(t *testing.T) {
	m, err := SetUpBpfProgram(t)
	require.NoError(t, err)
	t.Cleanup(m.Close)
	bpfMap, err := m.GetMap(bpfmaps.StackCountsMapName)
	require.NoError(t, err)

	stackID := int32(1234)

	// Delete should fail as the key doesn't exist.
	err = bpfMap.DeleteKey(unsafe.Pointer(&stackID))
	require.Error(t, err)
	require.ErrorIs(t, err, syscall.ENOENT)
}

func TestDeleteExistentKey(t *testing.T) {
	m, err := SetUpBpfProgram(t)
	require.NoError(t, err)
	t.Cleanup(m.Close)
	bpfMap, err := m.GetMap(bpfmaps.StackCountsMapName)
	require.NoError(t, err)

	stackID := int32(1234)

	// Insert some element that will be later deleted.
	value := []byte{'a'}
	err = bpfMap.Update(unsafe.Pointer(&stackID), unsafe.Pointer(&value[0]))
	require.NoError(t, err)

	// Delete should work.
	err = bpfMap.DeleteKey(unsafe.Pointer(&stackID))
	require.NoError(t, err)
}

func hasBatchOperations(t *testing.T) bool {
	t.Helper()

	m, err := SetUpBpfProgram(t)
	require.NoError(t, err)
	t.Cleanup(m.Close)
	bpfMap, err := m.GetMap(bpfmaps.StackCountsMapName)
	require.NoError(t, err)

	keys := make([]stackCountKey, bpfMap.MaxEntries())
	countKeysPtr := unsafe.Pointer(&keys[0])
	nextCountKey := uintptr(1)
	batchSize := bpfMap.MaxEntries()
	_, err = bpfMap.GetValueAndDeleteBatch(countKeysPtr, nil, unsafe.Pointer(&nextCountKey), batchSize)

	return err == nil
}

func TestGetValueAndDeleteBatchWithEmptyMap(t *testing.T) {
	if !hasBatchOperations(t) {
		t.Skip("Skipping testing of batched operations as they aren't supported")
	}

	m, err := SetUpBpfProgram(t)
	require.NoError(t, err)
	t.Cleanup(m.Close)
	bpfMap, err := m.GetMap(bpfmaps.StackCountsMapName)
	require.NoError(t, err)

	keys := make([]stackCountKey, bpfMap.MaxEntries())
	countKeysPtr := unsafe.Pointer(&keys[0])
	nextCountKey := uintptr(1)
	batchSize := bpfMap.MaxEntries()
	values, err := bpfMap.GetValueAndDeleteBatch(countKeysPtr, nil, unsafe.Pointer(&nextCountKey), batchSize)
	require.NoError(t, err)
	require.Empty(t, values)
}

func TestGetValueAndDeleteBatchFewerElementsThanCount(t *testing.T) {
	if !hasBatchOperations(t) {
		t.Skip("Skipping testing of batched operations as they aren't supported")
	}

	m, err := SetUpBpfProgram(t)
	require.NoError(t, err)
	t.Cleanup(m.Close)
	bpfMap, err := m.GetMap(bpfmaps.StackCountsMapName)
	require.NoError(t, err)

	stackID := int32(1234)

	// Insert some element that will be later deleted.
	value := []byte{'a'}
	err = bpfMap.Update(unsafe.Pointer(&stackID), unsafe.Pointer(&value[0]))
	require.NoError(t, err)

	// Request more elements than we have, this should return and delete everything.
	keys := make([]stackCountKey, bpfMap.MaxEntries())
	countKeysPtr := unsafe.Pointer(&keys[0])
	nextCountKey := uintptr(1)
	batchSize := bpfMap.MaxEntries()
	values, err := bpfMap.GetValueAndDeleteBatch(countKeysPtr, nil, unsafe.Pointer(&nextCountKey), batchSize)
	require.NoError(t, err)
	require.Len(t, values, 1)
}

func TestGetValueAndDeleteBatchExactElements(t *testing.T) {
	if !hasBatchOperations(t) {
		t.Skip("Skipping testing of batched operations as they aren't supported")
	}

	m, err := SetUpBpfProgram(t)
	require.NoError(t, err)
	t.Cleanup(m.Close)
	bpfMap, err := m.GetMap(bpfmaps.StackCountsMapName)
	require.NoError(t, err)

	stackID := int32(1234)

	// Insert some element that will be later deleted.
	value := []byte{'a'}
	err = bpfMap.Update(unsafe.Pointer(&stackID), unsafe.Pointer(&value[0]))
	require.NoError(t, err)

	// Request exactly the elements we have.
	keys := make([]stackCountKey, 1)
	countKeysPtr := unsafe.Pointer(&keys[0])
	nextCountKey := uintptr(1)
	batchSize := uint32(1)
	values, err := bpfMap.GetValueAndDeleteBatch(countKeysPtr, nil, unsafe.Pointer(&nextCountKey), batchSize)
	require.NoError(t, err)
	require.Len(t, values, 1)
}
