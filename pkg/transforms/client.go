package transforms

var transforms []*TransformDefinition

func SetupClient() {
	// Stagecoach East
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
		},
		Data: map[string]interface{}{
			"BrandColour": "#F2A83B",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCHU",
		},
		Data: map[string]interface{}{
			"BrandColour": "#F2A83B",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCBD",
		},
		Data: map[string]interface{}{
			"BrandColour": "#F2A83B",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCPB",
		},
		Data: map[string]interface{}{
			"BrandColour": "#F2A83B",
		},
	})
	// Whippet
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:WHIP",
		},
		Data: map[string]interface{}{
			"BrandColour": "#368BFF",
		},
	})

	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "A",
		},
		Data: map[string]interface{}{
			"BrandColour": "#04A387",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "B",
		},
		Data: map[string]interface{}{
			"BrandColour": "#04A387",
		},
	})

	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "PR1",
		},
		Data: map[string]interface{}{
			"BrandColour": "#E72D57",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "PR2",
		},
		Data: map[string]interface{}{
			"BrandColour": "#FF6500",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "PR3",
		},
		Data: map[string]interface{}{
			"BrandColour": "#4382B3",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "PR4",
		},
		Data: map[string]interface{}{
			"BrandColour": "#92BF73",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:SCCM",
			"ServiceName": "PR5",
		},
		Data: map[string]interface{}{
			"BrandColour": "#8547AC",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Operator",
		Match: map[string]string{
			"PrimaryIdentifier": "GB:NOCID:138416",
		},
		Data: map[string]interface{}{
			"Regions": []string{"UK:REGION:LONDON"},
		},
	})

	// TFL
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "Bakerloo",
		},
		Data: map[string]interface{}{
			"BrandColour": "#994f14",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "Central",
		},
		Data: map[string]interface{}{
			"BrandColour": "#d42e12",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "Circle",
		},
		Data: map[string]interface{}{
			"BrandColour": "#f7d117",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "District",
		},
		Data: map[string]interface{}{
			"BrandColour": "#007336",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "Hammersmith & City",
		},
		Data: map[string]interface{}{
			"BrandColour": "#eb9ca8",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "Jubilee",
		},
		Data: map[string]interface{}{
			"BrandColour": "#8c8f91",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "Metropolitan",
		},
		Data: map[string]interface{}{
			"BrandColour": "#8a004f",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "Northern",
		},
		Data: map[string]interface{}{
			"BrandColour": "#332b24",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "Piccadilly",
		},
		Data: map[string]interface{}{
			"BrandColour": "#2905a1",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "Victoria",
		},
		Data: map[string]interface{}{
			"BrandColour": "#00a3e0",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
	transforms = append(transforms, &TransformDefinition{
		Type: "ctdf.Service",
		Match: map[string]string{
			"OperatorRef": "GB:NOC:TFLO",
			"ServiceName": "Waterloo & City",
		},
		Data: map[string]interface{}{
			"BrandColour": "#7dd1b8",
			"BrandIcon":   "/icons/tfl-roundel-underground.svg",
		},
	})
}
