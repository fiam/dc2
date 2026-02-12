package api

import "time"

var (
	InstanceStatePending      = InstanceState{Code: "0", Name: "pending"}
	InstanceStateRunning      = InstanceState{Code: "16", Name: "running"}
	InstanceStateShuttingDown = InstanceState{Code: "32", Name: "shutting-down"}
	InstanceStateTerminated   = InstanceState{Code: "48", Name: "terminated"}
	InstanceStateStopping     = InstanceState{Code: "64", Name: "stopping"}
	InstanceStateStopped      = InstanceState{Code: "80", Name: "stopped"}
)

type DescribeInstancesResponse struct {
	ReservationSet []Reservation `xml:"reservationSet>item"`
}

type DescribeInstanceStatusResponse struct {
	InstanceStatusSet []InstanceStatus `xml:"instanceStatusSet>item"`
	NextToken         *string          `xml:"nextToken"`
}

type RunInstancesResponse struct {
	ReservationID string     `xml:"reservationId"`
	OwnerID       string     `xml:"ownerId"`
	InstancesSet  []Instance `xml:"instancesSet>item"`
}

type StopInstancesResponse struct {
	StoppingInstances []InstanceStateChange `xml:"instancesSet>item"`
}

type StartInstancesResponse struct {
	StartingInstances []InstanceStateChange `xml:"instancesSet>item"`
}

type TerminateInstancesResponse struct {
	TerminatingInstances []InstanceStateChange `xml:"instancesSet>item"`
}

type InstanceMetadataOptions struct {
	HTTPEndpoint *string `xml:"httpEndpoint"`
	State        *string `xml:"state"`
}

type ModifyInstanceMetadataOptionsResponse struct {
	InstanceID              *string                  `xml:"instanceId"`
	InstanceMetadataOptions *InstanceMetadataOptions `xml:"instanceMetadataOptions"`
}

type InstanceStateChange struct {
	InstanceID    string        `xml:"instanceId"`
	CurrentState  InstanceState `xml:"currentState"`
	PreviousState InstanceState `xml:"previousState"`
}

type Reservation struct {
	ReservationID string     `xml:"reservationId"`
	OwnerID       string     `xml:"ownerId"`
	InstancesSet  []Instance `xml:"instancesSet>item"`
}

type Instance struct {
	InstanceID            string                     `xml:"instanceId"`
	ImageID               string                     `xml:"imageId"`
	InstanceState         InstanceState              `xml:"instanceState"`
	StateTransitionReason string                     `xml:"reason"`
	StateReason           *StateReason               `xml:"stateReason"`
	PrivateDNSName        string                     `xml:"privateDnsName"`
	DNSName               string                     `xml:"dnsName"`
	KeyName               string                     `xml:"keyName"`
	AmiLaunchIndex        int                        `xml:"amiLaunchIndex"`
	InstanceType          string                     `xml:"instanceType"`
	LaunchTime            time.Time                  `xml:"launchTime"`
	Placement             Placement                  `xml:"placement"`
	Monitoring            Monitoring                 `xml:"monitoring"`
	SubnetID              string                     `xml:"subnetId"`
	VPCID                 string                     `xml:"vpcId"`
	PrivateIPAddress      string                     `xml:"privateIpAddress"`
	PublicIPAddress       string                     `xml:"ipAddress"`
	NetworkInterfaces     []InstanceNetworkInterface `xml:"networkInterfaceSet>item"`
	SecurityGroups        []Group                    `xml:"securityGroups>item"`
	Architecture          string                     `xml:"architecture"`
	RootDeviceType        string                     `xml:"rootDeviceType"`
	RootDeviceName        string                     `xml:"rootDeviceName"`
	TagSet                []Tag                      `xml:"tagSet>item"`
}

type StateReason struct {
	Code    string `xml:"code"`
	Message string `xml:"message"`
}

type InstanceNetworkInterface struct {
	NetworkInterfaceID string                                `xml:"networkInterfaceId"`
	MacAddress         string                                `xml:"macAddress"`
	Status             string                                `xml:"status"`
	SourceDestCheck    bool                                  `xml:"sourceDestCheck"`
	PrivateDNSName     string                                `xml:"privateDnsName"`
	PrivateIPAddress   string                                `xml:"privateIpAddress"`
	Association        *InstanceNetworkInterfaceAssociation  `xml:"association"`
	Attachment         *InstanceNetworkInterfaceAttachment   `xml:"attachment"`
	PrivateIPAddresses []InstancePrivateIPAddressAssociation `xml:"privateIpAddressesSet>item"`
}

type InstanceNetworkInterfaceAssociation struct {
	PublicDNSName string `xml:"publicDnsName"`
	PublicIP      string `xml:"publicIp"`
	IPOwnerID     string `xml:"ipOwnerId"`
}

type InstanceNetworkInterfaceAttachment struct {
	AttachmentID        string `xml:"attachmentId"`
	DeviceIndex         int    `xml:"deviceIndex"`
	Status              string `xml:"status"`
	DeleteOnTermination bool   `xml:"deleteOnTermination"`
}

type InstancePrivateIPAddressAssociation struct {
	PrivateDNSName string                               `xml:"privateDnsName"`
	PrivateIP      string                               `xml:"privateIpAddress"`
	Primary        bool                                 `xml:"primary"`
	Association    *InstanceNetworkInterfaceAssociation `xml:"association"`
}

type InstanceStatus struct {
	AvailabilityZone string        `xml:"availabilityZone"`
	InstanceID       string        `xml:"instanceId"`
	InstanceState    InstanceState `xml:"instanceState"`
	InstanceStatus   StatusSummary `xml:"instanceStatus"`
	SystemStatus     StatusSummary `xml:"systemStatus"`
}

type StatusSummary struct {
	Status  string         `xml:"status"`
	Details []StatusDetail `xml:"details>item"`
}

type StatusDetail struct {
	Name   string `xml:"name"`
	Status string `xml:"status"`
}

// InstanceState represents the state of an instance
type InstanceState struct {
	Code string `xml:"code"`
	Name string `xml:"name"`
}

// Placement represents the placement details of an instance
type Placement struct {
	AvailabilityZone string `url:"AvailabilityZone" xml:"availabilityZone"`
	Tenancy          string `xml:"tenancy"`
}

// Monitoring represents monitoring information of an instance
type Monitoring struct {
	State string `xml:"state"`
}

// Group represents a security group
type Group struct {
	GroupID   string `xml:"groupId"`
	GroupName string `xml:"groupName"`
}
