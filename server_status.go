package main

import (
	"os"
	"sync"
	"time"
)

type statusUsage struct {
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

type serverService struct {
	Name      string  `json:"name"`
	Memory    uint64  `json:"memory"`
	Uptime    float64 `json:"uptime"`
	Processes int     `json:"processes"`
}

type serviceStatus struct {
	Kind  string          `json:"kind"`
	Items []serverService `json:"items"`
	Total int             `json:"total"`
}

type serverStatus struct {
	Memory   *statusUsage   `json:"memory"`
	Disk     *statusUsage   `json:"disk"`
	Uptime   *float64       `json:"uptime"`
	Services *serviceStatus `json:"services,omitempty"`
}

const statusInterval = 100 * time.Millisecond

type serverMonitor struct {
	mu           sync.Mutex
	last         time.Time
	snapshot     serverStatus
	serviceMu    sync.Mutex
	lastServices time.Time
	services     *serviceStatus
}

func (m *serverMonitor) sample(root *os.File, includeServices bool) serverStatus {
	m.mu.Lock()
	now := time.Now()
	if now.Sub(m.last) >= statusInterval {
		m.last = now
		m.snapshot = readServerStatus(root)
	}
	snapshot := m.snapshot
	m.mu.Unlock()
	if includeServices {
		m.serviceMu.Lock()
		defer m.serviceMu.Unlock()
		now := time.Now()
		if now.Sub(m.lastServices) >= statusInterval {
			m.lastServices = now
			m.services = readServerServices(snapshot.Uptime)
		}
		snapshot.Services = m.services
	}
	return snapshot
}
