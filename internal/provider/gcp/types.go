package gcp

// GCE REST API types — just the fields we use.
// See https://cloud.google.com/compute/docs/reference/rest/v1/instances

type gceInstance struct {
	Name              string                `json:"name"`
	MachineType       string                `json:"machineType"`
	Status            string                `json:"status,omitempty"`
	Disks             []gceAttachedDisk     `json:"disks"`
	NetworkInterfaces []gceNetworkInterface `json:"networkInterfaces"`
	Labels            map[string]string     `json:"labels,omitempty"`
	Metadata          *gceMetadata          `json:"metadata,omitempty"`
	ServiceAccounts   []gceServiceAccount   `json:"serviceAccounts,omitempty"`
	Scheduling        *gceScheduling        `json:"scheduling,omitempty"`
}

type gceAttachedDisk struct {
	InitializeParams *gceDiskInitParams `json:"initializeParams,omitempty"`
	AutoDelete       bool               `json:"autoDelete"`
	Boot             bool               `json:"boot"`
	Type             string             `json:"type"`
}

type gceDiskInitParams struct {
	DiskSizeGb  int64  `json:"diskSizeGb"`
	SourceImage string `json:"sourceImage"`
}

type gceNetworkInterface struct {
	Network       string            `json:"network,omitempty"`
	Subnetwork    string            `json:"subnetwork,omitempty"`
	AccessConfigs []gceAccessConfig `json:"accessConfigs,omitempty"`
}

type gceAccessConfig struct {
	Name        string `json:"name"`
	NetworkTier string `json:"networkTier"`
	Type        string `json:"type"`
}

type gceMetadata struct {
	Items []gceMetadataItem `json:"items"`
}

type gceMetadataItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type gceServiceAccount struct {
	Email  string   `json:"email"`
	Scopes []string `json:"scopes"`
}

type gceScheduling struct {
	ProvisioningModel         string `json:"provisioningModel"`
	OnHostMaintenance         string `json:"onHostMaintenance"`
	AutomaticRestart          bool   `json:"automaticRestart"`
	InstanceTerminationAction string `json:"instanceTerminationAction"`
}

type gceOperation struct {
	Name   string       `json:"name"`
	Status string       `json:"status"`
	Error  *gceOpError  `json:"error,omitempty"`
}

type gceOpError struct {
	Errors []gceOpErrorEntry `json:"errors"`
}

type gceOpErrorEntry struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type gceInstanceList struct {
	Items         []gceInstance `json:"items"`
	NextPageToken string       `json:"nextPageToken,omitempty"`
}
