package dc2

import "encoding/xml"

type xmlErrorResponse struct {
	XMLName   xml.Name  `xml:"Response"`
	Errors    xmlErrors `xml:"Errors"`
	RequestID string    `xml:"RequestID"`
}

type xmlErrors struct {
	XMLName xml.Name `xml:"Errors"`
	Error   xmlError `xml:"Error"`
}

type xmlError struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}
