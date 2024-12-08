package dc2

import (
	"encoding/xml"
	"time"
)

type Response interface {
}

type DescribeInstancesResponse struct {
	XMLName        xml.Name      `xml:"DescribeInstancesResponse"`
	XMLNamespace   string        `xml:"xmlns,attr"`
	RequestID      string        `xml:"requestId"`
	ReservationSet []Reservation `xml:"reservationSet>item"`
}

type RunInstancesResponse struct {
	XMLName       xml.Name   `xml:"RunInstancesResponse"`
	XMLNamespace  string     `xml:"xmlns,attr"`
	RequestID     string     `xml:"requestId"`
	ReservationID string     `xml:"reservationId"`
	OwnerID       string     `xml:"ownerId"`
	InstancesSet  []Instance `xml:"instancesSet>item"`
}

type StopInstancesResponse struct {
	XMLName           xml.Name              `xml:"StopInstancesResponse"`
	XMLNamespace      string                `xml:"xmlns,attr"`
	RequestID         string                `xml:"requestId"`
	StoppingInstances []InstanceStateChange `xml:"instancesSet>item"`
}

type StartInstancesResponse struct {
	XMLName           xml.Name              `xml:"StartInstancesResponse"`
	XMLNamespace      string                `xml:"xmlns,attr"`
	RequestID         string                `xml:"requestId"`
	StartingInstances []InstanceStateChange `xml:"instancesSet>item"`
}

type TerminateInstancesResponse struct {
	XMLName              xml.Name              `xml:"TerminateInstancesResponse"`
	XMLNamespace         string                `xml:"xmlns,attr"`
	RequestID            string                `xml:"requestId"`
	TerminatingInstances []InstanceStateChange `xml:"instancesSet>item"`
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
	InstanceID       string        `xml:"instanceId"`
	ImageID          string        `xml:"imageId"`
	InstanceState    InstanceState `xml:"instanceState"`
	PrivateDNSName   string        `xml:"privateDnsName"`
	DNSName          string        `xml:"dnsName"`
	KeyName          string        `xml:"keyName"`
	AmiLaunchIndex   int           `xml:"amiLaunchIndex"`
	InstanceType     string        `xml:"instanceType"`
	LaunchTime       time.Time     `xml:"launchTime"`
	Placement        Placement     `xml:"placement"`
	Monitoring       Monitoring    `xml:"monitoring"`
	SubnetID         string        `xml:"subnetId"`
	VPCID            string        `xml:"vpcId"`
	PrivateIPAddress string        `xml:"privateIpAddress"`
	SecurityGroups   []Group       `xml:"securityGroups>item"`
	Architecture     string        `xml:"architecture"`
	RootDeviceType   string        `xml:"rootDeviceType"`
	RootDeviceName   string        `xml:"rootDeviceName"`
	TagSet           []Tag         `xml:"tagSet>item"`
}

// InstanceState represents the state of an instance
type InstanceState struct {
	Code string `xml:"code"`
	Name string `xml:"name"`
}

// Placement represents the placement details of an instance
type Placement struct {
	AvailabilityZone string `xml:"availabilityZone"`
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

// Tag represents a key-value tag pair
type Tag struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

var (
	InstanceStatePending      = InstanceState{Code: "0", Name: "pending"}
	InstanceStateRunning      = InstanceState{Code: "16", Name: "running"}
	InstanceStateShuttingDown = InstanceState{Code: "32", Name: "shutting-down"}
	InstanceStateTerminated   = InstanceState{Code: "48", Name: "terminated"}
	InstanceStateStopping     = InstanceState{Code: "64", Name: "stopping"}
	InstanceStateStopped      = InstanceState{Code: "80", Name: "stopped"}
)
