package nrod

type TrustMovement struct {
	EventType  string `json:"event_type"`
	TrainID    string `json:"train_id"`
	OperatorID string `json:"toc_id"`

	TimestampGBTT          string `json:"gbtt_timestamp"`
	PlannedTimestamp       string `json:"planned_timestamp"`
	ActualTimestamp        string `json:"actual_timestamp"`
	OriginalLocationStanox string `json:"original_loc_stanox"`
	OriginalLOCTimestamp   string `json:"original_loc_timestamp"`
	TimetableVariation     string `json:"timetable_variation"`
	CurrentTrainID         string `json:"current_train_id"`
	DelayMonitoringPoint   string `json:"delay_monitoring_point"`
	NextReportRunTime      string `json:"next_report_run_time"`
	ReportingStanox        string `json:"reporting_stanox"`
	CorrectionInd          string `json:"correction_ind"`
	EventSource            string `json:"event_source"`
	TrainFileAddress       string `json:"train_file_address"`
	Platform               string `json:"platform"`
	DivisionCode           string `json:"division_code"`
	TrainTerminated        string `json:"train_terminated"`
	Offroute               string `json:"offroute_ind"`
	VariationStatus        string `json:"variation_status"`
	TrainServiceCode       string `json:"train_service_code"`
	LocationStanox         string `json:"loc_stanox"`
	AutoExpected           string `json:"auto_expected"`
	Direction              string `json:"direction_ind"`
	Route                  string `json:"route"`
	PlannedEventType       string `json:"planned_event_type"`
	NextReportStanox       string `json:"next_report_stanox"`
	Line                   string `json:"line_ind"`
}
