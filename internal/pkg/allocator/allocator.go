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
	"fmt"
	"sync"
)

var (
	ErrNoAvailablePorts = errors.New("no available ports in range")
	ErrInvalidPortRange = errors.New("start port must be less than or equal to end port")
)

// PortAllocator manages dynamic port assignments for child exporter instances.
type PortAllocator struct {
	mu        sync.Mutex
	startPort int
	endPort   int
	allocated map[string]int // targetID -> port
	usedPorts map[int]string // port -> targetID
}

// NewPortAllocator creates a new allocator for the given inclusive port range.
func NewPortAllocator(startPort, endPort int) (*PortAllocator, error) {
	if startPort <= 0 || endPort <= 0 || startPort > endPort {
		return nil, fmt.Errorf("%w: [%d, %d]", ErrInvalidPortRange, startPort, endPort)
	}

	return &PortAllocator{
		startPort: startPort,
		endPort:   endPort,
		allocated: make(map[string]int),
		usedPorts: make(map[int]string),
	}, nil
}

// Allocate assigns a port to targetID. If targetID already has a port, it returns the existing port.
func (p *PortAllocator) Allocate(targetID string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if port, exists := p.allocated[targetID]; exists {
		return port, nil
	}

	for port := p.startPort; port <= p.endPort; port++ {
		if _, inUse := p.usedPorts[port]; !inUse {
			p.allocated[targetID] = port
			p.usedPorts[port] = targetID
			return port, nil
		}
	}

	return 0, fmt.Errorf("%w [%d-%d]", ErrNoAvailablePorts, p.startPort, p.endPort)
}

// Release frees the port assigned to targetID.
func (p *PortAllocator) Release(targetID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if port, exists := p.allocated[targetID]; exists {
		delete(p.allocated, targetID)
		delete(p.usedPorts, port)
	}
}

// Get returns the assigned port for targetID if one exists.
func (p *PortAllocator) Get(targetID string) (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	port, exists := p.allocated[targetID]
	return port, exists
}

// ActiveAllocations returns a copy of current targetID -> port mappings.
func (p *PortAllocator) ActiveAllocations() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()

	res := make(map[string]int, len(p.allocated))
	for k, v := range p.allocated {
		res[k] = v
	}
	return res
}
