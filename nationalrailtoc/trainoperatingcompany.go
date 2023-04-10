package nationalrailtoc

type TrainOperatingCompany struct {
	AtocCode        string
	AtocMember      bool
	StationOperator bool
	Name            string
	LegalName       string
	Logo            string
	CompanyWebsite  string

	CustomerService ContactDetails `xml:"SupportAndInformation>CustomerService>ContactDetails"`
}
