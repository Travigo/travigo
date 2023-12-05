package darwin

type FormationLoading struct {
	FID string `xml:"fid,attr"`
	RID string `xml:"rid,attr"`
	TPL string `xml:"tpl,attr"`
	WTA string `xml:"wta,attr"`
	WTD string `xml:"wtd,attr"`
	PTA string `xml:"pta,attr"`
	PTD string `xml:"ptd,attr"`

	Loading []FormationLoadingLoading `xml:"loading"`
}

type FormationLoadingLoading struct {
	CoachNumber       string `xml:"coachNumber,attr"`
	LoadingPercentage string `xml:",chardata"`
}
