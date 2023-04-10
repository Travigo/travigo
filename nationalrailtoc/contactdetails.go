package nationalrailtoc

type ContactDetails struct {
	Telephone    string `xml:"PrimaryTelephoneNumber>TelNationalNumber"`
	EmailAddress string

	PostalAddress PostalAddress
}

type PostalAddress struct {
	Line     []string
	PostCode string
}
