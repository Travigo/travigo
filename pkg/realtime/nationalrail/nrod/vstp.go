package nrod

type VSTPMessage struct {
	VSTP struct {
		MessageID string `json:"originMsgId"`
		Owner     string `json:"owner"`
		Timestamp string `json:"timestamp"`

		Schedule struct {
			ScheduleSegment []ScheduleSegment `json:"schedule_segment"`

			TransactionType string `json:"transaction_type"`
			TrainStatus     string `json:"train_status"`
			TrainUID        string `json:"CIF_train_uid"`
			STP             string `json:"CIF_stp_indicator"`
			Timetable       string `json:"applicable_timetable"`

			StartDate string `json:"schedule_start_date"`
			EndDate   string `json:"schedule_end_date"`
			DayRuns   string `json:"schedule_days_runs"`
		} `json:"schedule"`
	} `json:"VSTPCIFMsgV1"`
}

type ScheduleSegment struct {
	ScheduleLocations []ScheduleLocation `json:"schedule_location"`

	SignallingID     string `json:"signalling_id"`
	TrainServiceCode string `json:"CIF_train_service_code"`
	TrainCategory    string `json:"CIF_train_category"`
	Speed            string `json:"CIF_speed"`
	PowerType        string `json:"CIF_power_type"`
	CourseIndicator  string `json:"CIF_course_indicator"`
}

type ScheduleLocation struct {
	ScheduledPassTime      string `json:"scheduled_pass_time"`
	ScheduledDepartureTime string `json:"scheduled_departure_time"`
	ScheduledArrivalTime   string `json:"scheduled_arrival_time"`
	PublicDepartureTime    string `json:"public_departure_time"`
	PublicArrivalTime      string `json:"public_arrival_time"`
	Path                   string `json:"CIF_path"`
	Activity               string `json:"CIF_activity"`
	Platform               string `json:"CIF_platform"`
	Line                   string `json:"CIF_line"`
}
