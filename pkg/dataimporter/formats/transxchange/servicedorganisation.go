package transxchange

type ServicedOrganisation struct {
	ServicedOrganisationClassification string
	NatureOfOrganisation               string
	PhaseOfEducation                   string

	OrganisationCode string
	Name             string
	Note             string

	WorkingDays DatePattern
	Holidays    DatePattern

	DateExclusion []string

	ParentServicedOrganisationRef string
}

func findServicedOrganisation(ID string, servicedOrganisations []*ServicedOrganisation) *ServicedOrganisation {
	for _, org := range servicedOrganisations {
		if org.OrganisationCode == ID {
			return org
		}
	}

	return nil
}

type DatePattern struct {
	DateRange   []DateRange
	Description string

	DateExclusion string
}
