/*
 * Copyright (c) 2026, Sukkaito. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package allocator

import (
	"errors"
	"sync"
	"testing"
)

func TestPortAllocator(t *testing.T) {
	t.Run("invalid port range", func(t *testing.T) {
		_, err := NewPortAllocator(9500, 9400)
		if !errors.Is(err, ErrInvalidPortRange) {
			t.Fatalf("expected ErrInvalidPortRange, got: %v", err)
		}
	})

	t.Run("basic allocation and release", func(t *testing.T) {
		alloc, err := NewPortAllocator(9401, 9403)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		p1, err := alloc.Allocate("vm1")
		if err != nil || p1 != 9401 {
			t.Fatalf("expected 9401, got %d, err: %v", p1, err)
		}

		// Idempotent allocation for same target
		p1Repeat, err := alloc.Allocate("vm1")
		if err != nil || p1Repeat != 9401 {
			t.Fatalf("expected repeat 9401, got %d, err: %v", p1Repeat, err)
		}

		p2, err := alloc.Allocate("vm2")
		if err != nil || p2 != 9402 {
			t.Fatalf("expected 9402, got %d, err: %v", p2, err)
		}

		p3, err := alloc.Allocate("vm3")
		if err != nil || p3 != 9403 {
			t.Fatalf("expected 9403, got %d, err: %v", p3, err)
		}

		// Exhaustion
		_, err = alloc.Allocate("vm4")
		if !errors.Is(err, ErrNoAvailablePorts) {
			t.Fatalf("expected ErrNoAvailablePorts, got: %v", err)
		}

		// Release and reuse
		alloc.Release("vm2")
		p4, err := alloc.Allocate("vm4")
		if err != nil || p4 != 9402 {
			t.Fatalf("expected reused 9402, got %d, err: %v", p4, err)
		}

		// Check Get
		p, exists := alloc.Get("vm4")
		if !exists || p != 9402 {
			t.Fatalf("expected 9402 exists, got %d, exists: %v", p, exists)
		}
	})

	t.Run("concurrent allocation", func(t *testing.T) {
		alloc, err := NewPortAllocator(9000, 9100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var wg sync.WaitGroup
		targetCount := 50
		allocatedPorts := make(chan int, targetCount)

		for i := 0; i < targetCount; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				port, err := alloc.Allocate(string(rune('A' + id)))
				if err != nil {
					t.Errorf("allocation failed: %v", err)
					return
				}
				allocatedPorts <- port
			}(i)
		}

		wg.Wait()
		close(allocatedPorts)

		seen := make(map[int]bool)
		for p := range allocatedPorts {
			if seen[p] {
				t.Fatalf("duplicate port allocated: %d", p)
			}
			seen[p] = true
		}
	})
}
