package transforms

var transforms []*TransformDefinition

func SetupClient() {
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
		},
		Data: map[string]string{
			"BrandColour": "#F2A83B",
		},
	})

	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "A",
		},
		Data: map[string]string{
			"BrandColour": "#04A387",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "B",
		},
		Data: map[string]string{
			"BrandColour": "#04A387",
		},
	})

	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "PR1",
		},
		Data: map[string]string{
			"BrandColour": "#E72D57",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "PR2",
		},
		Data: map[string]string{
			"BrandColour": "#FF6500",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "PR3",
		},
		Data: map[string]string{
			"BrandColour": "#4382B3",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "PR4",
		},
		Data: map[string]string{
			"BrandColour": "#92BF73",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "PR5",
		},
		Data: map[string]string{
			"BrandColour": "#8547AC",
		},
	})
}
