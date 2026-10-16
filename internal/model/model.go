package model

import (
	"encoding/json"
	"time"
)

const SessionVersion = 1

type Session struct {
	Version          int             `json:"version"`
	ID               string          `json:"id"`
	ContainerID      string          `json:"container_id"`
	ContainerName    string          `json:"container_name"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	State            string          `json:"state"`
	ImagePath        string          `json:"image_path"`
	MountPoint       string          `json:"mount_point"`
	BaseDigest       string          `json:"base_digest"`
	Inspect          json.RawMessage `json:"inspect"`
	BackupName       string          `json:"backup_name,omitempty"`
	ReplacementID    string          `json:"replacement_id,omitempty"`
	ReplacementImage string          `json:"replacement_image,omitempty"`
}

type Manifest struct {
	Version     int               `json:"version"`
	ContainerID string            `json:"container_id"`
	CreatedAt   time.Time         `json:"created_at"`
	Entries     map[string]*Entry `json:"entries"`
}

type Entry struct {
	Name       string            `json:"name"`
	Mode       int64             `json:"mode"`
	UID        int               `json:"uid"`
	GID        int               `json:"gid"`
	Size       int64             `json:"size"`
	ModTime    time.Time         `json:"mod_time"`
	AccessTime time.Time         `json:"access_time,omitempty"`
	ChangeTime time.Time         `json:"change_time,omitempty"`
	Type       byte              `json:"type"`
	LinkName   string            `json:"link_name,omitempty"`
	Uname      string            `json:"uname,omitempty"`
	Gname      string            `json:"gname,omitempty"`
	DevMajor   int64             `json:"dev_major,omitempty"`
	DevMinor   int64             `json:"dev_minor,omitempty"`
	Xattrs     map[string]string `json:"xattrs,omitempty"`
	PAXRecords map[string]string `json:"pax_records,omitempty"`
	Format     int               `json:"format,omitempty"`
	SHA256     string            `json:"sha256,omitempty"`
	Virtual    bool              `json:"virtual,omitempty"`
}

type Change struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type Inspect struct {
	ID      string          `json:"Id"`
	Name    string          `json:"Name"`
	Image   string          `json:"Image"`
	Created string          `json:"Created"`
	Config  json.RawMessage `json:"Config"`
	State   struct {
		Running    bool   `json:"Running"`
		Status     string `json:"Status"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
	HostConfig      json.RawMessage `json:"HostConfig"`
	Mounts          []Mount         `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]EndpointSettings `json:"Networks"`
	} `json:"NetworkSettings"`
	SizeRootFS int64 `json:"SizeRootFs"`
}

type Mount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

type EndpointSettings struct {
	Aliases             []string            `json:"Aliases,omitempty"`
	Links               []string            `json:"Links,omitempty"`
	NetworkID           string              `json:"NetworkID,omitempty"`
	EndpointID          string              `json:"EndpointID,omitempty"`
	Gateway             string              `json:"Gateway,omitempty"`
	IPAddress           string              `json:"IPAddress,omitempty"`
	IPPrefixLen         int                 `json:"IPPrefixLen,omitempty"`
	IPv6Gateway         string              `json:"IPv6Gateway,omitempty"`
	GlobalIPv6Address   string              `json:"GlobalIPv6Address,omitempty"`
	GlobalIPv6PrefixLen int                 `json:"GlobalIPv6PrefixLen,omitempty"`
	MacAddress          string              `json:"MacAddress,omitempty"`
	DriverOpts          map[string]string   `json:"DriverOpts,omitempty"`
	IPAMConfig          *EndpointIPAMConfig `json:"IPAMConfig,omitempty"`
}

type EndpointIPAMConfig struct {
	IPv4Address  string   `json:"IPv4Address,omitempty"`
	IPv6Address  string   `json:"IPv6Address,omitempty"`
	LinkLocalIPs []string `json:"LinkLocalIPs,omitempty"`
}
