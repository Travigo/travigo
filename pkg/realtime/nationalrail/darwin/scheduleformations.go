package darwin

type ScheduleFormations struct {
	RID string `xml:"rid,attr"`

	Formations []Formation `xml:"formation"`
}

type Formation struct {
	FID string `xml:"fid,attr"`

	Coaches []FormationCoach `xml:"coaches>coach"`
}

type FormationCoach struct {
	Number string `xml:"coachNumber,attr"`
	Class  string `xml:"coachClass,attr"`

	Toilets []FormationCoachToilet `xml:"toilet"`
}

type FormationCoachToilet struct {
	Status string `xml:"coachNumber,attr"`
	Type   string `xml:",chardata"`
}
