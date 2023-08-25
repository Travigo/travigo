package railutils

import (
	"encoding/xml"
)

var LateReasons map[string]string
var CancelledReasons map[string]string

type lateCancelledReasons struct {
	LateReasons      []reason `xml:"LateRunningReasons>Reason"`
	CancelledReasons []reason `xml:"CancellationReasons>Reason"`
}
type reason struct {
	Code       string `xml:"code,attr"`
	ReasonText string `xml:"reasontext,attr"`
}

func LoadLateAndCancelledReasons() {
	LateReasons = map[string]string{}
	CancelledReasons = map[string]string{}

	var reasons lateCancelledReasons

	xml.Unmarshal([]byte(lateAndCancelledReasonsText), &reasons)

	for _, lateReason := range reasons.LateReasons {
		LateReasons[lateReason.Code] = lateReason.ReasonText
	}
	for _, cancelledReason := range reasons.CancelledReasons {
		CancelledReasons[cancelledReason.Code] = cancelledReason.ReasonText
	}
}

const lateAndCancelledReasonsText = `
  <xml>
	<LateRunningReasons>
    <Reason code="100" reasontext="This train has been delayed by a broken down train" />
    <Reason code="101" reasontext="This train has been delayed by a delay on a previous journey" />
    <Reason code="102" reasontext="This train has been delayed by a derailed train" />
    <Reason code="104" reasontext="This train has been delayed by a fire at a station" />
    <Reason code="105" reasontext="This train has been delayed by a fire at a station earlier" />
    <Reason code="106" reasontext="This train has been delayed by a landslip" />
    <Reason code="107" reasontext="This train has been delayed by a line-side fire" />
    <Reason code="108" reasontext="This train has been delayed by a member of train crew being unavailable" />
    <Reason code="109" reasontext="This train has been delayed by a passenger being taken ill" />
    <Reason code="110" reasontext="This train has been delayed by a passenger having been taken ill earlier" />
    <Reason code="111" reasontext="This train has been delayed by a person hit by a train" />
    <Reason code="112" reasontext="This train has been delayed by a person hit by a train earlier" />
    <Reason code="113" reasontext="This train has been delayed by a problem at a level crossing" />
    <Reason code="114" reasontext="This train has been delayed by a problem currently under investigation" />
    <Reason code="115" reasontext="This train has been delayed by a problem near the railway" />
    <Reason code="116" reasontext="This train has been delayed by a problem with a river bridge" />
    <Reason code="117" reasontext="This train has been delayed by a problem with line side equipment" />
    <Reason code="118" reasontext="This train has been delayed by a security alert" />
    <Reason code="119" reasontext="This train has been delayed by a train derailed earlier" />
    <Reason code="120" reasontext="This train has been delayed by a train fault" />
    <Reason code="121" reasontext="This train has been delayed by a train late from the depot" />
    <Reason code="122" reasontext="This train has been delayed by a train late from the depot earlier" />
    <Reason code="123" reasontext="This train has been delayed by a trespass incident" />
    <Reason code="124" reasontext="This train has been delayed by a vehicle striking a bridge" />
    <Reason code="125" reasontext="This train has been delayed by a vehicle striking a bridge earlier" />
    <Reason code="126" reasontext="This train has been delayed by an earlier broken down train" />
    <Reason code="128" reasontext="This train has been delayed by an earlier landslip" />
    <Reason code="129" reasontext="This train has been delayed by an earlier line-side fire" />
    <Reason code="130" reasontext="This train has been delayed by an earlier operating incident" />
    <Reason code="131" reasontext="This train has been delayed by an earlier problem at a level crossing" />
    <Reason code="132" reasontext="This train has been delayed by an earlier problem near the railway" />
    <Reason code="133" reasontext="This train has been delayed by an earlier problem with a river bridge" />
    <Reason code="134" reasontext="This train has been delayed by an earlier problem with line side equipment" />
    <Reason code="135" reasontext="This train has been delayed by an earlier security alert" />
    <Reason code="136" reasontext="This train has been delayed by an earlier train fault" />
    <Reason code="137" reasontext="This train has been delayed by an earlier trespass incident" />
    <Reason code="138" reasontext="This train has been delayed by an obstruction on the line" />
    <Reason code="139" reasontext="This train has been delayed by an obstruction on the line earlier" />
    <Reason code="140" reasontext="This train has been delayed by an operating incident" />
    <Reason code="141" reasontext="This train has been delayed by an unusually large passenger flow" />
    <Reason code="142" reasontext="This train has been delayed by an unusually large passenger flow earlier" />
    <Reason code="143" reasontext="This train has been delayed by animals on the line" />
    <Reason code="144" reasontext="This train has been delayed by animals on the line earlier" />
    <Reason code="145" reasontext="This train has been delayed by congestion caused by earlier delays" />
    <Reason code="146" reasontext="This train has been delayed by disruptive passengers" />
    <Reason code="147" reasontext="This train has been delayed by disruptive passengers earlier" />
    <Reason code="148" reasontext="This train has been delayed by earlier electrical supply problems" />
    <Reason code="149" reasontext="This train has been delayed by earlier emergency engineering works" />
    <Reason code="150" reasontext="This train has been delayed by earlier industrial action" />
    <Reason code="151" reasontext="This train has been delayed by earlier overhead wire problems" />
    <Reason code="152" reasontext="This train has been delayed by earlier over-running engineering works" />
    <Reason code="153" reasontext="This train has been delayed by earlier signalling problems" />
    <Reason code="154" reasontext="This train has been delayed by earlier vandalism" />
    <Reason code="155" reasontext="This train has been delayed by electrical supply problems" />
    <Reason code="156" reasontext="This train has been delayed by emergency engineering works" />
    <Reason code="157" reasontext="This train has been delayed by emergency services dealing with a prior incident" />
    <Reason code="158" reasontext="This train has been delayed by emergency services dealing with an incident" />
    <Reason code="159" reasontext="This train has been delayed by fire alarms sounding at a station" />
    <Reason code="160" reasontext="This train has been delayed by fire alarms sounding earlier at a station" />
    <Reason code="161" reasontext="This train has been delayed by flooding" />
    <Reason code="162" reasontext="This train has been delayed by flooding earlier" />
    <Reason code="163" reasontext="This train has been delayed by fog" />
    <Reason code="164" reasontext="This train has been delayed by fog earlier" />
    <Reason code="165" reasontext="This train has been delayed by high winds" />
    <Reason code="166" reasontext="This train has been delayed by high winds earlier" />
    <Reason code="167" reasontext="This train has been delayed by industrial action" />
    <Reason code="168" reasontext="This train has been delayed by lightning having damaged equipment" />
    <Reason code="169" reasontext="This train has been delayed by overhead wire problems" />
    <Reason code="170" reasontext="This train has been delayed by over-running engineering works" />
    <Reason code="171" reasontext="This train has been delayed by passengers transferring between trains" />
    <Reason code="172" reasontext="This train has been delayed by passengers transferring between trains earlier" />
    <Reason code="173" reasontext="This train has been delayed by poor rail conditions" />
    <Reason code="174" reasontext="This train has been delayed by poor rail conditions earlier" />
    <Reason code="175" reasontext="This train has been delayed by poor weather conditions" />
    <Reason code="176" reasontext="This train has been delayed by poor weather conditions earlier" />
    <Reason code="177" reasontext="This train has been delayed by safety checks being made" />
    <Reason code="178" reasontext="This train has been delayed by safety checks having been made earlier" />
    <Reason code="179" reasontext="This train has been delayed by signalling problems" />
    <Reason code="180" reasontext="This train has been delayed by snow" />
    <Reason code="181" reasontext="This train has been delayed by snow earlier" />
    <Reason code="182" reasontext="This train has been delayed by speed restrictions having been imposed" />
    <Reason code="183" reasontext="This train has been delayed by train crew having been unavailable earlier" />
    <Reason code="184" reasontext="This train has been delayed by vandalism" />
    <Reason code="185" reasontext="This train has been delayed by waiting earlier for a train crew member" />
    <Reason code="186" reasontext="This train has been delayed by waiting for a train crew member" />
    <Reason code="187" reasontext="This train has been delayed by engineering works" />
    <Reason code="501" reasontext="This train has been delayed by a broken down train" />
    <Reason code="502" reasontext="This train has been delayed by a broken windscreen on the train" />
    <Reason code="503" reasontext="This train has been delayed by a shortage of trains because of accident damage" />
    <Reason code="504" reasontext="This train has been delayed by a shortage of trains because of extra safety inspections" />
    <Reason code="505" reasontext="This train has been delayed by a shortage of trains because of vandalism" />
    <Reason code="506" reasontext="This train has been delayed by a shortage of trains following damage by snow and ice" />
    <Reason code="507" reasontext="This train has been delayed by more trains than usual needing repairs at the same time" />
    <Reason code="508" reasontext="This train has been delayed by the train for this service having broken down" />
    <Reason code="509" reasontext="This train has been delayed by this train breaking down" />
    <Reason code="510" reasontext="This train has been delayed by a collision between trains" />
    <Reason code="511" reasontext="This train has been delayed by a collision with the buffers at a station" />
    <Reason code="512" reasontext="This train has been delayed by a derailed train" />
    <Reason code="513" reasontext="This train has been delayed by a derailment within the depot" />
    <Reason code="514" reasontext="This train has been delayed by a low speed derailment" />
    <Reason code="515" reasontext="This train has been delayed by a train being involved in an accident" />
    <Reason code="516" reasontext="This train has been delayed by trains being involved in an accident" />
    <Reason code="517" reasontext="This train has been delayed by a fire at a station" />
    <Reason code="518" reasontext="This train has been delayed by a fire at a station earlier today" />
    <Reason code="519" reasontext="This train has been delayed by a landslip" />
    <Reason code="520" reasontext="This train has been delayed by a fire next to the track" />
    <Reason code="521" reasontext="This train has been delayed by a fire on a train" />
    <Reason code="522" reasontext="This train has been delayed by a member of on train staff being taken ill" />
    <Reason code="523" reasontext="This train has been delayed by a shortage of on train staff" />
    <Reason code="524" reasontext="This train has been delayed by a shortage of train conductors" />
    <Reason code="525" reasontext="This train has been delayed by a shortage of train crew" />
    <Reason code="526" reasontext="This train has been delayed by a shortage of train drivers" />
    <Reason code="527" reasontext="This train has been delayed by a shortage of train guards" />
    <Reason code="528" reasontext="This train has been delayed by a shortage of train managers" />
    <Reason code="529" reasontext="This train has been delayed by severe weather preventing train crew getting to work" />
    <Reason code="530" reasontext="This train has been delayed by the train conductor being taken ill" />
    <Reason code="531" reasontext="This train has been delayed by the train driver being taken ill" />
    <Reason code="532" reasontext="This train has been delayed by the train guard being taken ill" />
    <Reason code="533" reasontext="This train has been delayed by the train manager being taken ill" />
    <Reason code="534" reasontext="This train has been delayed by a passenger being taken ill at a station" />
    <Reason code="535" reasontext="This train has been delayed by a passenger being taken ill on a train" />
    <Reason code="536" reasontext="This train has been delayed by a passenger being taken ill on this train" />
    <Reason code="537" reasontext="This train has been delayed by a passenger being taken ill at a station earlier today" />
    <Reason code="538" reasontext="This train has been delayed by a passenger being taken ill on a train earlier today" />
    <Reason code="539" reasontext="This train has been delayed by a passenger being taken ill on this train earlier in its journey" />
    <Reason code="540" reasontext="This train has been delayed by a person being hit by a train" />
    <Reason code="541" reasontext="This train has been delayed by a person being hit by a train earlier today" />
    <Reason code="542" reasontext="This train has been delayed by a collision at a level crossing" />
    <Reason code="543" reasontext="This train has been delayed by a fault with barriers at a level crossing" />
    <Reason code="544" reasontext="This train has been delayed by a road accident at a level crossing" />
    <Reason code="545" reasontext="This train has been delayed by a road vehicle colliding with level crossing barriers" />
    <Reason code="546" reasontext="This train has been delayed by a road vehicle damaging track at a level crossing" />
    <Reason code="547" reasontext="This train has been delayed by a problem currently under investigation" />
    <Reason code="548" reasontext="This train has been delayed by a burst water main near the railway" />
    <Reason code="549" reasontext="This train has been delayed by a chemical spillage near the railway" />
    <Reason code="550" reasontext="This train has been delayed by a fire near the railway involving gas cylinders" />
    <Reason code="551" reasontext="This train has been delayed by a fire near the railway suspected to involve gas cylinders" />
    <Reason code="552" reasontext="This train has been delayed by a fire on property near the railway" />
    <Reason code="553" reasontext="This train has been delayed by a gas leak near the railway" />
    <Reason code="554" reasontext="This train has been delayed by a road accident near the railway" />
    <Reason code="555" reasontext="This train has been delayed by a wartime bomb near the railway" />
    <Reason code="556" reasontext="This train has been delayed by ambulance service dealing with an incident near the railway" />
    <Reason code="557" reasontext="This train has been delayed by emergency services dealing with an incident near the railway" />
    <Reason code="558" reasontext="This train has been delayed by fire brigade dealing with an incident near the railway" />
    <Reason code="559" reasontext="This train has been delayed by police dealing with an incident near the railway" />
    <Reason code="560" reasontext="This train has been delayed by a boat colliding with a bridge" />
    <Reason code="561" reasontext="This train has been delayed by a fault with a swing bridge over a river" />
    <Reason code="562" reasontext="This train has been delayed by a problem with a river bridge" />
    <Reason code="563" reasontext="This train has been delayed by a problem with line-side equipment" />
    <Reason code="564" reasontext="This train has been delayed by a security alert at a station" />
    <Reason code="565" reasontext="This train has been delayed by a security alert on another train" />
    <Reason code="566" reasontext="This train has been delayed by a security alert on this train" />
    <Reason code="567" reasontext="This train has been delayed by a train derailment earlier today" />
    <Reason code="568" reasontext="This train has been delayed by a train derailment yesterday" />
    <Reason code="569" reasontext="This train has been delayed by a fault occurring when attaching a part of a train" />
    <Reason code="570" reasontext="This train has been delayed by a fault occurring when attaching a part of this train" />
    <Reason code="571" reasontext="This train has been delayed by a fault occurring when detaching a part of a train" />
    <Reason code="572" reasontext="This train has been delayed by a fault occurring when detaching a part of this train" />
    <Reason code="573" reasontext="This train has been delayed by a fault on a train in front of this one" />
    <Reason code="574" reasontext="This train has been delayed by a fault on this train" />
    <Reason code="575" reasontext="This train has been delayed by this train being late from the depot" />
    <Reason code="576" reasontext="This train has been delayed by trespassers on the railway" />
    <Reason code="577" reasontext="This train has been delayed by a bus colliding with a bridge" />
    <Reason code="578" reasontext="This train has been delayed by a lorry colliding with a bridge" />
    <Reason code="579" reasontext="This train has been delayed by a road vehicle colliding with a bridge" />
    <Reason code="580" reasontext="This train has been delayed by a bus colliding with a bridge earlier on this train's journey" />
    <Reason code="581" reasontext="This train has been delayed by a bus colliding with a bridge earlier today" />
    <Reason code="582" reasontext="This train has been delayed by a lorry colliding with a bridge earlier on this train's journey" />
    <Reason code="583" reasontext="This train has been delayed by a lorry colliding with a bridge earlier today" />
    <Reason code="584" reasontext="This train has been delayed by a road vehicle colliding with a bridge earlier on this train's journey" />
    <Reason code="585" reasontext="This train has been delayed by a road vehicle colliding with a bridge earlier today" />
    <Reason code="586" reasontext="This train has been delayed by a broken down train earlier today" />
    <Reason code="587" reasontext="This train has been delayed by an earlier landslip" />
    <Reason code="588" reasontext="This train has been delayed by a fire next to the track earlier today" />
    <Reason code="589" reasontext="This train has been delayed by a fire on a train earlier today" />
    <Reason code="590" reasontext="This train has been delayed by a coach becoming uncoupled on a train earlier in its journey" />
    <Reason code="591" reasontext="This train has been delayed by a coach becoming uncoupled on a train earlier today" />
    <Reason code="592" reasontext="This train has been delayed by a coach becoming uncoupled on this train earlier in its journey" />
    <Reason code="593" reasontext="This train has been delayed by a coach becoming uncoupled on this train earlier today" />
    <Reason code="594" reasontext="This train has been delayed by a train not stopping at a station it was supposed to earlier in its journey" />
    <Reason code="595" reasontext="This train has been delayed by a train not stopping at a station it was supposed to earlier today" />
    <Reason code="596" reasontext="This train has been delayed by a train not stopping in the correct position at a station earlier in its journey" />
    <Reason code="597" reasontext="This train has been delayed by a train not stopping in the correct position at a station earlier today" />
    <Reason code="598" reasontext="This train has been delayed by a train's automatic braking system being activated earlier in its journey" />
    <Reason code="599" reasontext="This train has been delayed by a train's automatic braking system being activated earlier today" />
    <Reason code="600" reasontext="This train has been delayed by an operational incident earlier in its journey" />
    <Reason code="601" reasontext="This train has been delayed by an operational incident earlier today" />
    <Reason code="602" reasontext="This train has been delayed by this train not stopping at a station it was supposed to earlier in its journey" />
    <Reason code="603" reasontext="This train has been delayed by this train not stopping at a station it was supposed to earlier today" />
    <Reason code="604" reasontext="This train has been delayed by this train not stopping in the correct position at a station earlier in its journey" />
    <Reason code="605" reasontext="This train has been delayed by this train not stopping in the correct position at a station earlier today" />
    <Reason code="606" reasontext="This train has been delayed by this train's automatic braking system being activated earlier in its journey" />
    <Reason code="607" reasontext="This train has been delayed by this train's automatic braking system being activated earlier today" />
    <Reason code="608" reasontext="This train has been delayed by a collision at a level crossing earlier today" />
    <Reason code="609" reasontext="This train has been delayed by a collision at a level crossing yesterday" />
    <Reason code="610" reasontext="This train has been delayed by a fault with barriers at a level crossing earlier today" />
    <Reason code="611" reasontext="This train has been delayed by a fault with barriers at a level crossing yesterday" />
    <Reason code="612" reasontext="This train has been delayed by a road accident at a level crossing earlier today" />
    <Reason code="613" reasontext="This train has been delayed by a road accident at a level crossing yesterday" />
    <Reason code="614" reasontext="This train has been delayed by a road vehicle colliding with level crossing barriers earlier today" />
    <Reason code="615" reasontext="This train has been delayed by a road vehicle colliding with level crossing barriers yesterday" />
    <Reason code="616" reasontext="This train has been delayed by a road vehicle damaging track at a level crossing earlier today" />
    <Reason code="617" reasontext="This train has been delayed by a road vehicle damaging track at a level crossing yesterday" />
    <Reason code="618" reasontext="This train has been delayed by a burst water main near the railway earlier today" />
    <Reason code="619" reasontext="This train has been delayed by a burst water main near the railway yesterday" />
    <Reason code="620" reasontext="This train has been delayed by a chemical spillage near the railway earlier today" />
    <Reason code="621" reasontext="This train has been delayed by a chemical spillage near the railway yesterday" />
    <Reason code="622" reasontext="This train has been delayed by a fire near the railway involving gas cylinders earlier today" />
    <Reason code="623" reasontext="This train has been delayed by a fire near the railway involving gas cylinders yesterday" />
    <Reason code="624" reasontext="This train has been delayed by a fire near the railway suspected to involve gas cylinders earlier today" />
    <Reason code="625" reasontext="This train has been delayed by a fire near the railway suspected to involve gas cylinders yesterday" />
    <Reason code="626" reasontext="This train has been delayed by a fire on property near the railway earlier today" />
    <Reason code="627" reasontext="This train has been delayed by a fire on property near the railway yesterday" />
    <Reason code="628" reasontext="This train has been delayed by a gas leak near the railway earlier today" />
    <Reason code="629" reasontext="This train has been delayed by a gas leak near the railway yesterday" />
    <Reason code="630" reasontext="This train has been delayed by a road accident near the railway earlier today" />
    <Reason code="631" reasontext="This train has been delayed by a road accident near the railway yesterday" />
    <Reason code="632" reasontext="This train has been delayed by a wartime bomb near the railway earlier today" />
    <Reason code="633" reasontext="This train has been delayed by a wartime bomb near the railway yesterday" />
    <Reason code="634" reasontext="This train has been delayed by a wartime bomb which has now been made safe" />
    <Reason code="635" reasontext="This train has been delayed by ambulance service dealing with an incident near the railway earlier today" />
    <Reason code="636" reasontext="This train has been delayed by ambulance service dealing with an incident near the railway yesterday" />
    <Reason code="637" reasontext="This train has been delayed by emergency services dealing with an incident near the railway earlier today" />
    <Reason code="638" reasontext="This train has been delayed by emergency services dealing with an incident near the railway yesterday" />
    <Reason code="639" reasontext="This train has been delayed by fire brigade dealing with an incident near the railway earlier today" />
    <Reason code="640" reasontext="This train has been delayed by fire brigade dealing with an incident near the railway yesterday" />
    <Reason code="641" reasontext="This train has been delayed by police dealing with an incident near the railway earlier today" />
    <Reason code="642" reasontext="This train has been delayed by police dealing with an incident near the railway yesterday" />
    <Reason code="643" reasontext="This train has been delayed by a boat colliding with a bridge earlier today" />
    <Reason code="644" reasontext="This train has been delayed by a fault with a swing bridge over a river earlier today" />
    <Reason code="645" reasontext="This train has been delayed by a problem with a river bridge earlier today" />
    <Reason code="646" reasontext="This train has been delayed by an earlier problem with line-side equipment" />
    <Reason code="647" reasontext="This train has been delayed by a security alert earlier today" />
    <Reason code="648" reasontext="This train has been delayed by a fault on this train which is now fixed" />
    <Reason code="649" reasontext="This train has been delayed by trespassers on the railway earlier in this train's journey" />
    <Reason code="650" reasontext="This train has been delayed by trespassers on the railway earlier today" />
    <Reason code="651" reasontext="This train has been delayed by a bicycle on the track" />
    <Reason code="652" reasontext="This train has been delayed by a road vehicle blocking the railway" />
    <Reason code="653" reasontext="This train has been delayed by a supermarket trolley on the track" />
    <Reason code="654" reasontext="This train has been delayed by a train hitting an obstruction on the line" />
    <Reason code="655" reasontext="This train has been delayed by a tree blocking the railway" />
    <Reason code="656" reasontext="This train has been delayed by an obstruction on the track" />
    <Reason code="657" reasontext="This train has been delayed by checking reports of an obstruction on the line" />
    <Reason code="658" reasontext="This train has been delayed by this train hitting an obstruction on the line" />
    <Reason code="659" reasontext="This train has been delayed by a bicycle on the track earlier on this train's journey" />
    <Reason code="660" reasontext="This train has been delayed by a bicycle on the track earlier today" />
    <Reason code="661" reasontext="This train has been delayed by a road vehicle blocking the railway earlier on this train's journey" />
    <Reason code="662" reasontext="This train has been delayed by a road vehicle blocking the railway earlier today" />
    <Reason code="663" reasontext="This train has been delayed by a supermarket trolley on the track earlier on this train's journey" />
    <Reason code="664" reasontext="This train has been delayed by a supermarket trolley on the track earlier today" />
    <Reason code="665" reasontext="This train has been delayed by a train hitting an obstruction on the line earlier on this train's journey" />
    <Reason code="666" reasontext="This train has been delayed by a train hitting an obstruction on the line earlier today" />
    <Reason code="667" reasontext="This train has been delayed by a tree blocking the railway earlier on this train's journey" />
    <Reason code="668" reasontext="This train has been delayed by a tree blocking the railway earlier today" />
    <Reason code="669" reasontext="This train has been delayed by an obstruction on the track earlier on this train's journey" />
    <Reason code="670" reasontext="This train has been delayed by an obstruction on the track earlier today" />
    <Reason code="671" reasontext="This train has been delayed by checking reports of an obstruction on the line earlier on this train's journey" />
    <Reason code="672" reasontext="This train has been delayed by checking reports of an obstruction on the line earlier today" />
    <Reason code="673" reasontext="This train has been delayed by this train hitting an obstruction on the line earlier in its journey" />
    <Reason code="674" reasontext="This train has been delayed by this train hitting an obstruction on the line earlier on this train's journey" />
    <Reason code="675" reasontext="This train has been delayed by this train hitting an obstruction on the line earlier today" />
    <Reason code="676" reasontext="This train has been delayed by a coach becoming uncoupled on a train" />
    <Reason code="677" reasontext="This train has been delayed by a coach becoming uncoupled on this train" />
    <Reason code="678" reasontext="This train has been delayed by a train not stopping at a station it was supposed to" />
    <Reason code="679" reasontext="This train has been delayed by a train not stopping in the correct position at a station" />
    <Reason code="680" reasontext="This train has been delayed by a train's automatic braking system being activated" />
    <Reason code="681" reasontext="This train has been delayed by an operational incident" />
    <Reason code="682" reasontext="This train has been delayed by this train not stopping at a station it was supposed to" />
    <Reason code="683" reasontext="This train has been delayed by this train not stopping in the correct position at a station" />
    <Reason code="684" reasontext="This train has been delayed by this train's automatic braking system being activated" />
    <Reason code="685" reasontext="This train has been delayed by overcrowding" />
    <Reason code="686" reasontext="This train has been delayed by overcrowding as this train has fewer coaches than normal" />
    <Reason code="687" reasontext="This train has been delayed by overcrowding because an earlier train had fewer coaches than normal" />
    <Reason code="688" reasontext="This train has been delayed by overcrowding because of a concert" />
    <Reason code="689" reasontext="This train has been delayed by overcrowding because of a football match" />
    <Reason code="690" reasontext="This train has been delayed by overcrowding because of a marathon" />
    <Reason code="691" reasontext="This train has been delayed by overcrowding because of a rugby match" />
    <Reason code="692" reasontext="This train has been delayed by overcrowding because of a sporting event" />
    <Reason code="693" reasontext="This train has been delayed by overcrowding because of an earlier cancellation" />
    <Reason code="694" reasontext="This train has been delayed by overcrowding because of an event" />
    <Reason code="695" reasontext="This train has been delayed by overcrowding earlier on this train's journey" />
    <Reason code="696" reasontext="This train has been delayed by animals on the railway" />
    <Reason code="697" reasontext="This train has been delayed by cattle on the railway" />
    <Reason code="698" reasontext="This train has been delayed by horses on the railway" />
    <Reason code="699" reasontext="This train has been delayed by sheep on the railway" />
    <Reason code="700" reasontext="This train has been delayed by animals on the railway earlier today" />
    <Reason code="701" reasontext="This train has been delayed by cattle on the railway earlier today" />
    <Reason code="702" reasontext="This train has been delayed by horses on the railway earlier today" />
    <Reason code="703" reasontext="This train has been delayed by sheep on the railway earlier today" />
    <Reason code="704" reasontext="This train has been delayed by passengers causing a disturbance on a train" />
    <Reason code="705" reasontext="This train has been delayed by passengers causing a disturbance on this train" />
    <Reason code="706" reasontext="This train has been delayed by passengers causing a disturbance earlier in this train's journey" />
    <Reason code="707" reasontext="This train has been delayed by passengers causing a disturbance on a train earlier today" />
    <Reason code="708" reasontext="This train has been delayed by a fault with the electric third rail earlier on this train's journey" />
    <Reason code="709" reasontext="This train has been delayed by a fault with the electric third rail earlier today" />
    <Reason code="710" reasontext="This train has been delayed by damage to the electric third rail earlier on this train's journey" />
    <Reason code="711" reasontext="This train has been delayed by damage to the electric third rail earlier today" />
    <Reason code="712" reasontext="This train has been delayed by failure of the electricity supply earlier on this train's journey" />
    <Reason code="713" reasontext="This train has been delayed by failure of the electricity supply earlier today" />
    <Reason code="714" reasontext="This train has been delayed by the electricity being switched off for safety reasons earlier on this train's journey" />
    <Reason code="715" reasontext="This train has been delayed by the electricity being switched off for safety reasons earlier today" />
    <Reason code="716" reasontext="This train has been delayed by urgent repairs to a bridge earlier today" />
    <Reason code="717" reasontext="This train has been delayed by urgent repairs to a tunnel earlier today" />
    <Reason code="718" reasontext="This train has been delayed by urgent repairs to the railway earlier today" />
    <Reason code="719" reasontext="This train has been delayed by urgent repairs to the track earlier today" />
    <Reason code="720" reasontext="This train has been delayed by expected industrial action earlier today" />
    <Reason code="721" reasontext="This train has been delayed by expected industrial action yesterday" />
    <Reason code="722" reasontext="This train has been delayed by industrial action earlier today" />
    <Reason code="723" reasontext="This train has been delayed by industrial action yesterday" />
    <Reason code="724" reasontext="This train has been delayed by an object being caught on the overhead electric wires earlier on this train's journey" />
    <Reason code="725" reasontext="This train has been delayed by an object being caught on the overhead electric wires earlier today" />
    <Reason code="726" reasontext="This train has been delayed by damage to the overhead electric wires earlier on this train's journey" />
    <Reason code="727" reasontext="This train has been delayed by damage to the overhead electric wires earlier today" />
    <Reason code="728" reasontext="This train has been delayed by earlier engineering works not being finished on time" />
    <Reason code="729" reasontext="This train has been delayed by a fault with the on train signalling system earlier on this train's journey" />
    <Reason code="730" reasontext="This train has been delayed by a fault with the on train signalling system earlier today" />
    <Reason code="733" reasontext="This train has been delayed by a fault with the signalling system earlier on this train's journey" />
    <Reason code="734" reasontext="This train has been delayed by a fault with the signalling system earlier today" />
    <Reason code="737" reasontext="This train has been delayed by the fire alarm sounding in a signalbox earlier on this train's journey" />
    <Reason code="738" reasontext="This train has been delayed by the fire alarm sounding in a signalbox earlier today" />
    <Reason code="739" reasontext="This train has been delayed by the fire alarm sounding in the signalling centre earlier on this train's journey" />
    <Reason code="740" reasontext="This train has been delayed by the fire alarm sounding in the signalling centre earlier today" />
    <Reason code="741" reasontext="This train has been delayed by attempted theft of overhead line electrification equipment earlier today" />
    <Reason code="742" reasontext="This train has been delayed by attempted theft of overhead line electrification equipment yesterday" />
    <Reason code="743" reasontext="This train has been delayed by attempted theft of railway equipment earlier today" />
    <Reason code="744" reasontext="This train has been delayed by attempted theft of railway equipment yesterday" />
    <Reason code="745" reasontext="This train has been delayed by attempted theft of signalling cables earlier today" />
    <Reason code="746" reasontext="This train has been delayed by attempted theft of signalling cables yesterday" />
    <Reason code="747" reasontext="This train has been delayed by attempted theft of third rail electrification equipment earlier today" />
    <Reason code="748" reasontext="This train has been delayed by attempted theft of third rail electrification equipment yesterday" />
    <Reason code="749" reasontext="This train has been delayed by theft of overhead line electrification equipment earlier today" />
    <Reason code="750" reasontext="This train has been delayed by theft of overhead line electrification equipment yesterday" />
    <Reason code="751" reasontext="This train has been delayed by theft of railway equipment earlier today" />
    <Reason code="752" reasontext="This train has been delayed by theft of railway equipment yesterday" />
    <Reason code="753" reasontext="This train has been delayed by theft of signalling cables earlier today" />
    <Reason code="754" reasontext="This train has been delayed by theft of signalling cables yesterday" />
    <Reason code="755" reasontext="This train has been delayed by theft of third rail electrification equipment earlier today" />
    <Reason code="756" reasontext="This train has been delayed by theft of third rail electrification equipment yesterday" />
    <Reason code="757" reasontext="This train has been delayed by vandalism at a station earlier today" />
    <Reason code="758" reasontext="This train has been delayed by vandalism at a station yesterday" />
    <Reason code="759" reasontext="This train has been delayed by vandalism of railway equipment earlier today" />
    <Reason code="760" reasontext="This train has been delayed by vandalism of railway equipment yesterday" />
    <Reason code="761" reasontext="This train has been delayed by vandalism on a train earlier today" />
    <Reason code="762" reasontext="This train has been delayed by vandalism on a train yesterday" />
    <Reason code="763" reasontext="This train has been delayed by vandalism on this train earlier today" />
    <Reason code="764" reasontext="This train has been delayed by vandalism on this train yesterday" />
    <Reason code="765" reasontext="This train has been delayed by a fault with the electric third rail" />
    <Reason code="766" reasontext="This train has been delayed by damage to the electric third rail" />
    <Reason code="767" reasontext="This train has been delayed by failure of the electricity supply" />
    <Reason code="768" reasontext="This train has been delayed by the electricity being switched off for safety reasons" />
    <Reason code="769" reasontext="This train has been delayed by urgent repairs to a bridge" />
    <Reason code="770" reasontext="This train has been delayed by urgent repairs to a tunnel" />
    <Reason code="771" reasontext="This train has been delayed by urgent repairs to the railway" />
    <Reason code="772" reasontext="This train has been delayed by urgent repairs to the track" />
    <Reason code="773" reasontext="This train has been delayed by the emergency services dealing with an incident earlier today" />
    <Reason code="774" reasontext="This train has been delayed by ambulance service dealing with an incident" />
    <Reason code="775" reasontext="This train has been delayed by fire brigade dealing with an incident" />
    <Reason code="776" reasontext="This train has been delayed by police dealing with an incident" />
    <Reason code="777" reasontext="This train has been delayed by the emergency services dealing with an incident" />
    <Reason code="778" reasontext="This train has been delayed by the fire alarm sounding at a station" />
    <Reason code="779" reasontext="This train has been delayed by the fire alarm sounding at a station earlier today" />
    <Reason code="780" reasontext="This train has been delayed by a burst water main flooding the railway" />
    <Reason code="781" reasontext="This train has been delayed by a river flooding the railway" />
    <Reason code="782" reasontext="This train has been delayed by flood water making the railway potentially unsafe" />
    <Reason code="783" reasontext="This train has been delayed by flooding" />
    <Reason code="784" reasontext="This train has been delayed by heavy rain flooding the railway" />
    <Reason code="785" reasontext="This train has been delayed by predicted flooding" />
    <Reason code="786" reasontext="This train has been delayed by the sea flooding the railway" />
    <Reason code="787" reasontext="This train has been delayed by a burst water main flooding the railway earlier today" />
    <Reason code="788" reasontext="This train has been delayed by a river flooding the railway earlier today" />
    <Reason code="789" reasontext="This train has been delayed by flood water making the railway potentially unsafe earlier today" />
    <Reason code="790" reasontext="This train has been delayed by flooding earlier in this train's journey" />
    <Reason code="791" reasontext="This train has been delayed by flooding earlier today" />
    <Reason code="792" reasontext="This train has been delayed by heavy rain flooding the railway earlier today" />
    <Reason code="793" reasontext="This train has been delayed by predicted flooding earlier today" />
    <Reason code="794" reasontext="This train has been delayed by the sea flooding the railway earlier today" />
    <Reason code="795" reasontext="This train has been delayed by thick fog" />
    <Reason code="796" reasontext="This train has been delayed by thick fog earlier in this train's journey" />
    <Reason code="797" reasontext="This train has been delayed by thick fog earlier today" />
    <Reason code="798" reasontext="This train has been delayed by forecasted high winds" />
    <Reason code="799" reasontext="This train has been delayed by high winds" />
    <Reason code="800" reasontext="This train has been delayed by high winds earlier in this train's journey" />
    <Reason code="801" reasontext="This train has been delayed by high winds earlier today" />
    <Reason code="802" reasontext="This train has been delayed by expected industrial action" />
    <Reason code="803" reasontext="This train has been delayed by industrial action" />
    <Reason code="804" reasontext="This train has been delayed by lightning damaging a station" />
    <Reason code="805" reasontext="This train has been delayed by lightning damaging a train" />
    <Reason code="806" reasontext="This train has been delayed by lightning damaging equipment" />
    <Reason code="807" reasontext="This train has been delayed by lightning damaging the electricity supply" />
    <Reason code="808" reasontext="This train has been delayed by lightning damaging the signalling system" />
    <Reason code="809" reasontext="This train has been delayed by lightning damaging this train" />
    <Reason code="810" reasontext="This train has been delayed by an object being caught on the overhead electric wires" />
    <Reason code="811" reasontext="This train has been delayed by damage to the overhead electric wires" />
    <Reason code="812" reasontext="This train has been delayed by engineering works not being finished on time" />
    <Reason code="813" reasontext="This train has been delayed by forecasted slippery rails" />
    <Reason code="814" reasontext="This train has been delayed by ice preventing this train getting electricity from the third rail" />
    <Reason code="815" reasontext="This train has been delayed by ice preventing trains getting electricity from the third rail" />
    <Reason code="816" reasontext="This train has been delayed by slippery rails" />
    <Reason code="817" reasontext="This train has been delayed by slippery rails earlier in this train's journey" />
    <Reason code="818" reasontext="This train has been delayed by slippery rails earlier today" />
    <Reason code="819" reasontext="This train has been delayed by forecasted severe weather" />
    <Reason code="820" reasontext="This train has been delayed by severe weather" />
    <Reason code="821" reasontext="This train has been delayed by severe weather earlier" />
    <Reason code="822" reasontext="This train has been delayed by severe weather earlier in this train's journey" />
    <Reason code="823" reasontext="This train has been delayed by severe weather earlier today" />
    <Reason code="824" reasontext="This train has been delayed by a safety inspection of the track" />
    <Reason code="825" reasontext="This train has been delayed by a safety inspection on a train" />
    <Reason code="826" reasontext="This train has been delayed by a safety inspection on this train" />
    <Reason code="827" reasontext="This train has been delayed by a safety inspection of the track earlier today" />
    <Reason code="828" reasontext="This train has been delayed by a safety inspection on a train earlier today" />
    <Reason code="829" reasontext="This train has been delayed by a safety inspection on this train earlier in its journey" />
    <Reason code="830" reasontext="This train has been delayed by a fault with the on train signalling system" />
    <Reason code="831" reasontext="This train has been delayed by a fault with the radio system between the driver and the signaller" />
    <Reason code="832" reasontext="This train has been delayed by a fault with the signalling system" />
    <Reason code="834" reasontext="This train has been delayed by the fire alarm sounding in a signalbox" />
    <Reason code="835" reasontext="This train has been delayed by the fire alarm sounding in the signalling centre" />
    <Reason code="836" reasontext="This train has been delayed by forecasted heavy snow" />
    <Reason code="837" reasontext="This train has been delayed by heavy snow" />
    <Reason code="838" reasontext="This train has been delayed by heavy snow earlier in this train's journey" />
    <Reason code="839" reasontext="This train has been delayed by heavy snow earlier today" />
    <Reason code="840" reasontext="This train has been delayed by heavy snow over recent days" />
    <Reason code="841" reasontext="This train has been delayed by a speed restriction" />
    <Reason code="842" reasontext="This train has been delayed by a speed restriction because of fog" />
    <Reason code="843" reasontext="This train has been delayed by a speed restriction because of fog earlier on this train's journey" />
    <Reason code="844" reasontext="This train has been delayed by a speed restriction because of fog earlier today" />
    <Reason code="845" reasontext="This train has been delayed by a speed restriction because of heavy rain" />
    <Reason code="846" reasontext="This train has been delayed by a speed restriction because of heavy rain earlier on this train's journey" />
    <Reason code="847" reasontext="This train has been delayed by a speed restriction because of heavy rain earlier today" />
    <Reason code="848" reasontext="This train has been delayed by a speed restriction because of high track temperatures" />
    <Reason code="849" reasontext="This train has been delayed by a speed restriction because of high track temperatures earlier on this train's journey" />
    <Reason code="850" reasontext="This train has been delayed by a speed restriction because of high track temperatures earlier today" />
    <Reason code="851" reasontext="This train has been delayed by a speed restriction because of high winds" />
    <Reason code="852" reasontext="This train has been delayed by a speed restriction because of high winds earlier on this train's journey" />
    <Reason code="853" reasontext="This train has been delayed by a speed restriction because of high winds earlier today" />
    <Reason code="854" reasontext="This train has been delayed by a speed restriction because of severe weather" />
    <Reason code="855" reasontext="This train has been delayed by a speed restriction because of severe weather earlier on this train's journey" />
    <Reason code="856" reasontext="This train has been delayed by a speed restriction because of severe weather earlier today" />
    <Reason code="857" reasontext="This train has been delayed by a speed restriction because of snow and ice" />
    <Reason code="858" reasontext="This train has been delayed by a speed restriction because of snow and ice earlier on this train's journey" />
    <Reason code="859" reasontext="This train has been delayed by a speed restriction because of snow and ice earlier today" />
    <Reason code="860" reasontext="This train has been delayed by a speed restriction earlier on this train's journey" />
    <Reason code="861" reasontext="This train has been delayed by a speed restriction earlier today" />
    <Reason code="862" reasontext="This train has been delayed by a speed restriction in a tunnel" />
    <Reason code="863" reasontext="This train has been delayed by a speed restriction in a tunnel earlier on this train's journey" />
    <Reason code="864" reasontext="This train has been delayed by a speed restriction in a tunnel earlier today" />
    <Reason code="865" reasontext="This train has been delayed by a speed restriction over a bridge" />
    <Reason code="866" reasontext="This train has been delayed by a speed restriction over a bridge earlier on this train's journey" />
    <Reason code="867" reasontext="This train has been delayed by a speed restriction over a bridge earlier today" />
    <Reason code="868" reasontext="This train has been delayed by a speed restriction over an embankment" />
    <Reason code="869" reasontext="This train has been delayed by a speed restriction over an embankment earlier on this train's journey" />
    <Reason code="870" reasontext="This train has been delayed by a speed restriction over an embankment earlier today" />
    <Reason code="871" reasontext="This train has been delayed by a speed restriction over defective track" />
    <Reason code="872" reasontext="This train has been delayed by a speed restriction over defective track earlier on this train's journey" />
    <Reason code="873" reasontext="This train has been delayed by a speed restriction over defective track earlier today" />
    <Reason code="874" reasontext="This train has been delayed by attempted theft of overhead line electrification equipment" />
    <Reason code="875" reasontext="This train has been delayed by attempted theft of railway equipment" />
    <Reason code="876" reasontext="This train has been delayed by attempted theft of signalling cables" />
    <Reason code="877" reasontext="This train has been delayed by attempted theft of third rail electrification equipment" />
    <Reason code="878" reasontext="This train has been delayed by theft of overhead line electrification equipment" />
    <Reason code="879" reasontext="This train has been delayed by theft of railway equipment" />
    <Reason code="880" reasontext="This train has been delayed by theft of signalling cables" />
    <Reason code="881" reasontext="This train has been delayed by theft of third rail electrification equipment" />
    <Reason code="882" reasontext="This train has been delayed by vandalism at a station" />
    <Reason code="883" reasontext="This train has been delayed by vandalism of railway equipment" />
    <Reason code="884" reasontext="This train has been delayed by vandalism on a train" />
    <Reason code="885" reasontext="This train has been delayed by vandalism on this train" />
    <Reason code="886" reasontext="This train has been delayed by train crew being delayed" />
    <Reason code="887" reasontext="This train has been delayed by train crew being delayed by service disruption" />
    <Reason code="888" reasontext="This train has been delayed by a bridge being damaged" />
    <Reason code="889" reasontext="This train has been delayed by a bridge being damaged by a boat" />
    <Reason code="890" reasontext="This train has been delayed by a bridge being damaged by a road vehicle" />
    <Reason code="891" reasontext="This train has been delayed by a bridge having collapsed" />
    <Reason code="892" reasontext="This train has been delayed by a broken rail" />
    <Reason code="893" reasontext="This train has been delayed by a late departure while the train was cleaned specially" />
    <Reason code="894" reasontext="This train has been delayed by a late running freight train" />
    <Reason code="895" reasontext="This train has been delayed by a late running train being in front of this one" />
    <Reason code="896" reasontext="This train has been delayed by a points failure" />
    <Reason code="897" reasontext="This train has been delayed by a power cut at the station" />
    <Reason code="898" reasontext="This train has been delayed by a problem with the station lighting" />
    <Reason code="899" reasontext="This train has been delayed by a rail buckling in the heat" />
    <Reason code="900" reasontext="This train has been delayed by a railway embankment being damaged" />
    <Reason code="901" reasontext="This train has been delayed by a shortage of station staff" />
    <Reason code="902" reasontext="This train has been delayed by a tunnel being closed for safety reasons" />
    <Reason code="903" reasontext="This train has been delayed by an incident at the airport" />
    <Reason code="904" reasontext="This train has been delayed by congestion" />
    <Reason code="905" reasontext="This train has been delayed by the communication alarm being activated on a train" />
    <Reason code="906" reasontext="This train has been delayed by the communication alarm being activated on this train" />
    <Reason code="907" reasontext="This train has been delayed by the train departing late to maintain customer connections" />
    <Reason code="908" reasontext="This train has been delayed by the train making extra stops because a train was cancelled" />
    <Reason code="909" reasontext="This train has been delayed by the train making extra stops because of service disruption" />
    <Reason code="910" reasontext="This train has been delayed by waiting for a part of the train to be attached" />
    <Reason code="911" reasontext="This train has been delayed by a fault on a train" />
    <Reason code="912" reasontext="This train has been delayed by a problem with platform equipment" />
    <Reason code="913" reasontext="This train has been delayed by a fault on a train earlier" />
    <Reason code="914" reasontext="This train has been delayed by issues with communication systems" />
    <Reason code="915" reasontext="This train has been delayed by a problem in the depot" />
    <Reason code="916" reasontext="This train has been delayed by signalling staff being unavailable" />
    <Reason code="917" reasontext="This train has been delayed by staff training" />
    <Reason code="918" reasontext="This train has been delayed by a short-notice change to the timetable" />
    <Reason code="919" reasontext="This train has been delayed by misuse of a level crossing" />
    <Reason code="920" reasontext="This train has been delayed by a member of train crew being unavailable" />
    <Reason code="921" reasontext="This train has been delayed by a member of train crew being unavailable earlier" />
  </LateRunningReasons>
	<CancellationReasons>
    <Reason code="100" reasontext="This train has been cancelled because of a broken down train" />
    <Reason code="101" reasontext="This train has been cancelled because of a delay on a previous journey" />
    <Reason code="102" reasontext="This train has been cancelled because of a derailed train" />
    <Reason code="104" reasontext="This train has been cancelled because of a fire at a station" />
    <Reason code="105" reasontext="This train has been cancelled because of a fire at a station earlier" />
    <Reason code="106" reasontext="This train has been cancelled because of a landslip" />
    <Reason code="107" reasontext="This train has been cancelled because of a line-side fire" />
    <Reason code="108" reasontext="This train has been cancelled because of a member of train crew being unavailable" />
    <Reason code="109" reasontext="This train has been cancelled because of a passenger being taken ill" />
    <Reason code="110" reasontext="This train has been cancelled because of a passenger having been taken ill earlier" />
    <Reason code="111" reasontext="This train has been cancelled because of a person hit by a train" />
    <Reason code="112" reasontext="This train has been cancelled because of a person hit by a train earlier" />
    <Reason code="113" reasontext="This train has been cancelled because of a problem at a level crossing" />
    <Reason code="114" reasontext="This train has been cancelled because of a problem currently under investigation" />
    <Reason code="115" reasontext="This train has been cancelled because of a problem near the railway" />
    <Reason code="116" reasontext="This train has been cancelled because of a problem with a river bridge" />
    <Reason code="117" reasontext="This train has been cancelled because of a problem with line side equipment" />
    <Reason code="118" reasontext="This train has been cancelled because of a security alert" />
    <Reason code="119" reasontext="This train has been cancelled because of a train derailed earlier" />
    <Reason code="120" reasontext="This train has been cancelled because of a train fault" />
    <Reason code="121" reasontext="This train has been cancelled because of a train late from the depot" />
    <Reason code="122" reasontext="This train has been cancelled because of a train late from the depot earlier" />
    <Reason code="123" reasontext="This train has been cancelled because of a trespass incident" />
    <Reason code="124" reasontext="This train has been cancelled because of a vehicle striking a bridge" />
    <Reason code="125" reasontext="This train has been cancelled because of a vehicle striking a bridge earlier" />
    <Reason code="126" reasontext="This train has been cancelled because of an earlier broken down train" />
    <Reason code="128" reasontext="This train has been cancelled because of an earlier landslip" />
    <Reason code="129" reasontext="This train has been cancelled because of an earlier line-side fire" />
    <Reason code="130" reasontext="This train has been cancelled because of an earlier operating incident" />
    <Reason code="131" reasontext="This train has been cancelled because of an earlier problem at a level crossing" />
    <Reason code="132" reasontext="This train has been cancelled because of an earlier problem near the railway" />
    <Reason code="133" reasontext="This train has been cancelled because of an earlier problem with a river bridge" />
    <Reason code="134" reasontext="This train has been cancelled because of an earlier problem with line side equipment" />
    <Reason code="135" reasontext="This train has been cancelled because of an earlier security alert" />
    <Reason code="136" reasontext="This train has been cancelled because of an earlier train fault" />
    <Reason code="137" reasontext="This train has been cancelled because of an earlier trespass incident" />
    <Reason code="138" reasontext="This train has been cancelled because of an obstruction on the line" />
    <Reason code="139" reasontext="This train has been cancelled because of an obstruction on the line earlier" />
    <Reason code="140" reasontext="This train has been cancelled because of an operating incident" />
    <Reason code="141" reasontext="This train has been cancelled because of an unusually large passenger flow" />
    <Reason code="142" reasontext="This train has been cancelled because of an unusually large passenger flow earlier" />
    <Reason code="143" reasontext="This train has been cancelled because of animals on the line" />
    <Reason code="144" reasontext="This train has been cancelled because of animals on the line earlier" />
    <Reason code="145" reasontext="This train has been cancelled because of congestion caused by earlier delays" />
    <Reason code="146" reasontext="This train has been cancelled because of disruptive passengers" />
    <Reason code="147" reasontext="This train has been cancelled because of disruptive passengers earlier" />
    <Reason code="148" reasontext="This train has been cancelled because of earlier electrical supply problems" />
    <Reason code="149" reasontext="This train has been cancelled because of earlier emergency engineering works" />
    <Reason code="150" reasontext="This train has been cancelled because of earlier industrial action" />
    <Reason code="151" reasontext="This train has been cancelled because of earlier overhead wire problems" />
    <Reason code="152" reasontext="This train has been cancelled because of earlier over-running engineering works" />
    <Reason code="153" reasontext="This train has been cancelled because of earlier signalling problems" />
    <Reason code="154" reasontext="This train has been cancelled because of earlier vandalism" />
    <Reason code="155" reasontext="This train has been cancelled because of electrical supply problems" />
    <Reason code="156" reasontext="This train has been cancelled because of emergency engineering works" />
    <Reason code="157" reasontext="This train has been cancelled because of emergency services dealing with a prior incident" />
    <Reason code="158" reasontext="This train has been cancelled because of emergency services dealing with an incident" />
    <Reason code="159" reasontext="This train has been cancelled because of fire alarms sounding at a station" />
    <Reason code="160" reasontext="This train has been cancelled because of fire alarms sounding earlier at a station" />
    <Reason code="161" reasontext="This train has been cancelled because of flooding" />
    <Reason code="162" reasontext="This train has been cancelled because of flooding earlier" />
    <Reason code="163" reasontext="This train has been cancelled because of fog" />
    <Reason code="164" reasontext="This train has been cancelled because of fog earlier" />
    <Reason code="165" reasontext="This train has been cancelled because of high winds" />
    <Reason code="166" reasontext="This train has been cancelled because of high winds earlier" />
    <Reason code="167" reasontext="This train has been cancelled because of industrial action" />
    <Reason code="168" reasontext="This train has been cancelled because of lightning having damaged equipment" />
    <Reason code="169" reasontext="This train has been cancelled because of overhead wire problems" />
    <Reason code="170" reasontext="This train has been cancelled because of over-running engineering works" />
    <Reason code="171" reasontext="This train has been cancelled because of passengers transferring between trains" />
    <Reason code="172" reasontext="This train has been cancelled because of passengers transferring between trains earlier" />
    <Reason code="173" reasontext="This train has been cancelled because of poor rail conditions" />
    <Reason code="174" reasontext="This train has been cancelled because of poor rail conditions earlier" />
    <Reason code="175" reasontext="This train has been cancelled because of poor weather conditions" />
    <Reason code="176" reasontext="This train has been cancelled because of poor weather conditions earlier" />
    <Reason code="177" reasontext="This train has been cancelled because of safety checks being made" />
    <Reason code="178" reasontext="This train has been cancelled because of safety checks having been made earlier" />
    <Reason code="179" reasontext="This train has been cancelled because of signalling problems" />
    <Reason code="180" reasontext="This train has been cancelled because of snow" />
    <Reason code="181" reasontext="This train has been cancelled because of snow earlier" />
    <Reason code="182" reasontext="This train has been cancelled because of speed restrictions having been imposed" />
    <Reason code="183" reasontext="This train has been cancelled because of train crew having been unavailable earlier" />
    <Reason code="184" reasontext="This train has been cancelled because of vandalism" />
    <Reason code="185" reasontext="This train has been cancelled because of waiting earlier for a train crew member" />
    <Reason code="186" reasontext="This train has been cancelled because of waiting for a train crew member" />
    <Reason code="187" reasontext="This train has been cancelled because of engineering works" />
    <Reason code="501" reasontext="This train has been cancelled because of a broken down train" />
    <Reason code="502" reasontext="This train has been cancelled because of a broken windscreen on the train" />
    <Reason code="503" reasontext="This train has been cancelled because of a shortage of trains because of accident damage" />
    <Reason code="504" reasontext="This train has been cancelled because of a shortage of trains because of extra safety inspections" />
    <Reason code="505" reasontext="This train has been cancelled because of a shortage of trains because of vandalism" />
    <Reason code="506" reasontext="This train has been cancelled because of a shortage of trains following damage by snow and ice" />
    <Reason code="507" reasontext="This train has been cancelled because of more trains than usual needing repairs at the same time" />
    <Reason code="508" reasontext="This train has been cancelled because of the train for this service having broken down" />
    <Reason code="509" reasontext="This train has been cancelled because of this train breaking down" />
    <Reason code="510" reasontext="This train has been cancelled because of a collision between trains" />
    <Reason code="511" reasontext="This train has been cancelled because of a collision with the buffers at a station" />
    <Reason code="512" reasontext="This train has been cancelled because of a derailed train" />
    <Reason code="513" reasontext="This train has been cancelled because of a derailment within the depot" />
    <Reason code="514" reasontext="This train has been cancelled because of a low speed derailment" />
    <Reason code="515" reasontext="This train has been cancelled because of a train being involved in an accident" />
    <Reason code="516" reasontext="This train has been cancelled because of trains being involved in an accident" />
    <Reason code="517" reasontext="This train has been cancelled because of a fire at a station" />
    <Reason code="518" reasontext="This train has been cancelled because of a fire at a station earlier today" />
    <Reason code="519" reasontext="This train has been cancelled because of a landslip" />
    <Reason code="520" reasontext="This train has been cancelled because of a fire next to the track" />
    <Reason code="521" reasontext="This train has been cancelled because of a fire on a train" />
    <Reason code="522" reasontext="This train has been cancelled because of a member of on train staff being taken ill" />
    <Reason code="523" reasontext="This train has been cancelled because of a shortage of on train staff" />
    <Reason code="524" reasontext="This train has been cancelled because of a shortage of train conductors" />
    <Reason code="525" reasontext="This train has been cancelled because of a shortage of train crew" />
    <Reason code="526" reasontext="This train has been cancelled because of a shortage of train drivers" />
    <Reason code="527" reasontext="This train has been cancelled because of a shortage of train guards" />
    <Reason code="528" reasontext="This train has been cancelled because of a shortage of train managers" />
    <Reason code="529" reasontext="This train has been cancelled because of severe weather preventing train crew getting to work" />
    <Reason code="530" reasontext="This train has been cancelled because of the train conductor being taken ill" />
    <Reason code="531" reasontext="This train has been cancelled because of the train driver being taken ill" />
    <Reason code="532" reasontext="This train has been cancelled because of the train guard being taken ill" />
    <Reason code="533" reasontext="This train has been cancelled because of the train manager being taken ill" />
    <Reason code="534" reasontext="This train has been cancelled because of a passenger being taken ill at a station" />
    <Reason code="535" reasontext="This train has been cancelled because of a passenger being taken ill on a train" />
    <Reason code="536" reasontext="This train has been cancelled because of a passenger being taken ill on this train" />
    <Reason code="537" reasontext="This train has been cancelled because of a passenger being taken ill at a station earlier today" />
    <Reason code="538" reasontext="This train has been cancelled because of a passenger being taken ill on a train earlier today" />
    <Reason code="539" reasontext="This train has been cancelled because of a passenger being taken ill on this train earlier in its journey" />
    <Reason code="540" reasontext="This train has been cancelled because of a person being hit by a train" />
    <Reason code="541" reasontext="This train has been cancelled because of a person being hit by a train earlier today" />
    <Reason code="542" reasontext="This train has been cancelled because of a collision at a level crossing" />
    <Reason code="543" reasontext="This train has been cancelled because of a fault with barriers at a level crossing" />
    <Reason code="544" reasontext="This train has been cancelled because of a road accident at a level crossing" />
    <Reason code="545" reasontext="This train has been cancelled because of a road vehicle colliding with level crossing barriers" />
    <Reason code="546" reasontext="This train has been cancelled because of a road vehicle damaging track at a level crossing" />
    <Reason code="547" reasontext="This train has been cancelled because of a problem currently under investigation" />
    <Reason code="548" reasontext="This train has been cancelled because of a burst water main near the railway" />
    <Reason code="549" reasontext="This train has been cancelled because of a chemical spillage near the railway" />
    <Reason code="550" reasontext="This train has been cancelled because of a fire near the railway involving gas cylinders" />
    <Reason code="551" reasontext="This train has been cancelled because of a fire near the railway suspected to involve gas cylinders" />
    <Reason code="552" reasontext="This train has been cancelled because of a fire on property near the railway" />
    <Reason code="553" reasontext="This train has been cancelled because of a gas leak near the railway" />
    <Reason code="554" reasontext="This train has been cancelled because of a road accident near the railway" />
    <Reason code="555" reasontext="This train has been cancelled because of a wartime bomb near the railway" />
    <Reason code="556" reasontext="This train has been cancelled because of ambulance service dealing with an incident near the railway" />
    <Reason code="557" reasontext="This train has been cancelled because of emergency services dealing with an incident near the railway" />
    <Reason code="558" reasontext="This train has been cancelled because of fire brigade dealing with an incident near the railway" />
    <Reason code="559" reasontext="This train has been cancelled because of police dealing with an incident near the railway" />
    <Reason code="560" reasontext="This train has been cancelled because of a boat colliding with a bridge" />
    <Reason code="561" reasontext="This train has been cancelled because of a fault with a swing bridge over a river" />
    <Reason code="562" reasontext="This train has been cancelled because of a problem with a river bridge" />
    <Reason code="563" reasontext="This train has been cancelled because of a problem with line-side equipment" />
    <Reason code="564" reasontext="This train has been cancelled because of a security alert at a station" />
    <Reason code="565" reasontext="This train has been cancelled because of a security alert on another train" />
    <Reason code="566" reasontext="This train has been cancelled because of a security alert on this train" />
    <Reason code="567" reasontext="This train has been cancelled because of a train derailment earlier today" />
    <Reason code="568" reasontext="This train has been cancelled because of a train derailment yesterday" />
    <Reason code="569" reasontext="This train has been cancelled because of a fault occurring when attaching a part of a train" />
    <Reason code="570" reasontext="This train has been cancelled because of a fault occurring when attaching a part of this train" />
    <Reason code="571" reasontext="This train has been cancelled because of a fault occurring when detaching a part of a train" />
    <Reason code="572" reasontext="This train has been cancelled because of a fault occurring when detaching a part of this train" />
    <Reason code="573" reasontext="This train has been cancelled because of a fault on a train in front of this one" />
    <Reason code="574" reasontext="This train has been cancelled because of a fault on this train" />
    <Reason code="575" reasontext="This train has been cancelled because of this train being late from the depot" />
    <Reason code="576" reasontext="This train has been cancelled because of trespassers on the railway" />
    <Reason code="577" reasontext="This train has been cancelled because of a bus colliding with a bridge" />
    <Reason code="578" reasontext="This train has been cancelled because of a lorry colliding with a bridge" />
    <Reason code="579" reasontext="This train has been cancelled because of a road vehicle colliding with a bridge" />
    <Reason code="580" reasontext="This train has been cancelled because of a bus colliding with a bridge earlier on this train's journey" />
    <Reason code="581" reasontext="This train has been cancelled because of a bus colliding with a bridge earlier today" />
    <Reason code="582" reasontext="This train has been cancelled because of a lorry colliding with a bridge earlier on this train's journey" />
    <Reason code="583" reasontext="This train has been cancelled because of a lorry colliding with a bridge earlier today" />
    <Reason code="584" reasontext="This train has been cancelled because of a road vehicle colliding with a bridge earlier on this train's journey" />
    <Reason code="585" reasontext="This train has been cancelled because of a road vehicle colliding with a bridge earlier today" />
    <Reason code="586" reasontext="This train has been cancelled because of a broken down train earlier today" />
    <Reason code="587" reasontext="This train has been cancelled because of an earlier landslip" />
    <Reason code="588" reasontext="This train has been cancelled because of a fire next to the track earlier today" />
    <Reason code="589" reasontext="This train has been cancelled because of a fire on a train earlier today" />
    <Reason code="590" reasontext="This train has been cancelled because of a coach becoming uncoupled on a train earlier in its journey" />
    <Reason code="591" reasontext="This train has been cancelled because of a coach becoming uncoupled on a train earlier today" />
    <Reason code="592" reasontext="This train has been cancelled because of a coach becoming uncoupled on this train earlier in its journey" />
    <Reason code="593" reasontext="This train has been cancelled because of a coach becoming uncoupled on this train earlier today" />
    <Reason code="594" reasontext="This train has been cancelled because of a train not stopping at a station it was supposed to earlier in its journey" />
    <Reason code="595" reasontext="This train has been cancelled because of a train not stopping at a station it was supposed to earlier today" />
    <Reason code="596" reasontext="This train has been cancelled because of a train not stopping in the correct position at a station earlier in its journey" />
    <Reason code="597" reasontext="This train has been cancelled because of a train not stopping in the correct position at a station earlier today" />
    <Reason code="598" reasontext="This train has been cancelled because of a train's automatic braking system being activated earlier in its journey" />
    <Reason code="599" reasontext="This train has been cancelled because of a train's automatic braking system being activated earlier today" />
    <Reason code="600" reasontext="This train has been cancelled because of an operational incident earlier in its journey" />
    <Reason code="601" reasontext="This train has been cancelled because of an operational incident earlier today" />
    <Reason code="602" reasontext="This train has been cancelled because of this train not stopping at a station it was supposed to earlier in its journey" />
    <Reason code="603" reasontext="This train has been cancelled because of this train not stopping at a station it was supposed to earlier today" />
    <Reason code="604" reasontext="This train has been cancelled because of this train not stopping in the correct position at a station earlier in its journey" />
    <Reason code="605" reasontext="This train has been cancelled because of this train not stopping in the correct position at a station earlier today" />
    <Reason code="606" reasontext="This train has been cancelled because of this train's automatic braking system being activated earlier in its journey" />
    <Reason code="607" reasontext="This train has been cancelled because of this train's automatic braking system being activated earlier today" />
    <Reason code="608" reasontext="This train has been cancelled because of a collision at a level crossing earlier today" />
    <Reason code="609" reasontext="This train has been cancelled because of a collision at a level crossing yesterday" />
    <Reason code="610" reasontext="This train has been cancelled because of a fault with barriers at a level crossing earlier today" />
    <Reason code="611" reasontext="This train has been cancelled because of a fault with barriers at a level crossing yesterday" />
    <Reason code="612" reasontext="This train has been cancelled because of a road accident at a level crossing earlier today" />
    <Reason code="613" reasontext="This train has been cancelled because of a road accident at a level crossing yesterday" />
    <Reason code="614" reasontext="This train has been cancelled because of a road vehicle colliding with level crossing barriers earlier today" />
    <Reason code="615" reasontext="This train has been cancelled because of a road vehicle colliding with level crossing barriers yesterday" />
    <Reason code="616" reasontext="This train has been cancelled because of a road vehicle damaging track at a level crossing earlier today" />
    <Reason code="617" reasontext="This train has been cancelled because of a road vehicle damaging track at a level crossing yesterday" />
    <Reason code="618" reasontext="This train has been cancelled because of a burst water main near the railway earlier today" />
    <Reason code="619" reasontext="This train has been cancelled because of a burst water main near the railway yesterday" />
    <Reason code="620" reasontext="This train has been cancelled because of a chemical spillage near the railway earlier today" />
    <Reason code="621" reasontext="This train has been cancelled because of a chemical spillage near the railway yesterday" />
    <Reason code="622" reasontext="This train has been cancelled because of a fire near the railway involving gas cylinders earlier today" />
    <Reason code="623" reasontext="This train has been cancelled because of a fire near the railway involving gas cylinders yesterday" />
    <Reason code="624" reasontext="This train has been cancelled because of a fire near the railway suspected to involve gas cylinders earlier today" />
    <Reason code="625" reasontext="This train has been cancelled because of a fire near the railway suspected to involve gas cylinders yesterday" />
    <Reason code="626" reasontext="This train has been cancelled because of a fire on property near the railway earlier today" />
    <Reason code="627" reasontext="This train has been cancelled because of a fire on property near the railway yesterday" />
    <Reason code="628" reasontext="This train has been cancelled because of a gas leak near the railway earlier today" />
    <Reason code="629" reasontext="This train has been cancelled because of a gas leak near the railway yesterday" />
    <Reason code="630" reasontext="This train has been cancelled because of a road accident near the railway earlier today" />
    <Reason code="631" reasontext="This train has been cancelled because of a road accident near the railway yesterday" />
    <Reason code="632" reasontext="This train has been cancelled because of a wartime bomb near the railway earlier today" />
    <Reason code="633" reasontext="This train has been cancelled because of a wartime bomb near the railway yesterday" />
    <Reason code="634" reasontext="This train has been cancelled because of a wartime bomb which has now been made safe" />
    <Reason code="635" reasontext="This train has been cancelled because of ambulance service dealing with an incident near the railway earlier today" />
    <Reason code="636" reasontext="This train has been cancelled because of ambulance service dealing with an incident near the railway yesterday" />
    <Reason code="637" reasontext="This train has been cancelled because of emergency services dealing with an incident near the railway earlier today" />
    <Reason code="638" reasontext="This train has been cancelled because of emergency services dealing with an incident near the railway yesterday" />
    <Reason code="639" reasontext="This train has been cancelled because of fire brigade dealing with an incident near the railway earlier today" />
    <Reason code="640" reasontext="This train has been cancelled because of fire brigade dealing with an incident near the railway yesterday" />
    <Reason code="641" reasontext="This train has been cancelled because of police dealing with an incident near the railway earlier today" />
    <Reason code="642" reasontext="This train has been cancelled because of police dealing with an incident near the railway yesterday" />
    <Reason code="643" reasontext="This train has been cancelled because of a boat colliding with a bridge earlier today" />
    <Reason code="644" reasontext="This train has been cancelled because of a fault with a swing bridge over a river earlier today" />
    <Reason code="645" reasontext="This train has been cancelled because of a problem with a river bridge earlier today" />
    <Reason code="646" reasontext="This train has been cancelled because of an earlier problem with line-side equipment" />
    <Reason code="647" reasontext="This train has been cancelled because of a security alert earlier today" />
    <Reason code="648" reasontext="This train has been cancelled because of a fault on this train which is now fixed" />
    <Reason code="649" reasontext="This train has been cancelled because of trespassers on the railway earlier in this train's journey" />
    <Reason code="650" reasontext="This train has been cancelled because of trespassers on the railway earlier today" />
    <Reason code="651" reasontext="This train has been cancelled because of a bicycle on the track" />
    <Reason code="652" reasontext="This train has been cancelled because of a road vehicle blocking the railway" />
    <Reason code="653" reasontext="This train has been cancelled because of a supermarket trolley on the track" />
    <Reason code="654" reasontext="This train has been cancelled because of a train hitting an obstruction on the line" />
    <Reason code="655" reasontext="This train has been cancelled because of a tree blocking the railway" />
    <Reason code="656" reasontext="This train has been cancelled because of an obstruction on the track" />
    <Reason code="657" reasontext="This train has been cancelled because of checking reports of an obstruction on the line" />
    <Reason code="658" reasontext="This train has been cancelled because of this train hitting an obstruction on the line" />
    <Reason code="659" reasontext="This train has been cancelled because of a bicycle on the track earlier on this train's journey" />
    <Reason code="660" reasontext="This train has been cancelled because of a bicycle on the track earlier today" />
    <Reason code="661" reasontext="This train has been cancelled because of a road vehicle blocking the railway earlier on this train's journey" />
    <Reason code="662" reasontext="This train has been cancelled because of a road vehicle blocking the railway earlier today" />
    <Reason code="663" reasontext="This train has been cancelled because of a supermarket trolley on the track earlier on this train's journey" />
    <Reason code="664" reasontext="This train has been cancelled because of a supermarket trolley on the track earlier today" />
    <Reason code="665" reasontext="This train has been cancelled because of a train hitting an obstruction on the line earlier on this train's journey" />
    <Reason code="666" reasontext="This train has been cancelled because of a train hitting an obstruction on the line earlier today" />
    <Reason code="667" reasontext="This train has been cancelled because of a tree blocking the railway earlier on this train's journey" />
    <Reason code="668" reasontext="This train has been cancelled because of a tree blocking the railway earlier today" />
    <Reason code="669" reasontext="This train has been cancelled because of an obstruction on the track earlier on this train's journey" />
    <Reason code="670" reasontext="This train has been cancelled because of an obstruction on the track earlier today" />
    <Reason code="671" reasontext="This train has been cancelled because of checking reports of an obstruction on the line earlier on this train's journey" />
    <Reason code="672" reasontext="This train has been cancelled because of checking reports of an obstruction on the line earlier today" />
    <Reason code="673" reasontext="This train has been cancelled because of this train hitting an obstruction on the line earlier in its journey" />
    <Reason code="674" reasontext="This train has been cancelled because of this train hitting an obstruction on the line earlier on this train's journey" />
    <Reason code="675" reasontext="This train has been cancelled because of this train hitting an obstruction on the line earlier today" />
    <Reason code="676" reasontext="This train has been cancelled because of a coach becoming uncoupled on a train" />
    <Reason code="677" reasontext="This train has been cancelled because of a coach becoming uncoupled on this train" />
    <Reason code="678" reasontext="This train has been cancelled because of a train not stopping at a station it was supposed to" />
    <Reason code="679" reasontext="This train has been cancelled because of a train not stopping in the correct position at a station" />
    <Reason code="680" reasontext="This train has been cancelled because of a train's automatic braking system being activated" />
    <Reason code="681" reasontext="This train has been cancelled because of an operational incident" />
    <Reason code="682" reasontext="This train has been cancelled because of this train not stopping at a station it was supposed to" />
    <Reason code="683" reasontext="This train has been cancelled because of this train not stopping in the correct position at a station" />
    <Reason code="684" reasontext="This train has been cancelled because of this train's automatic braking system being activated" />
    <Reason code="685" reasontext="This train has been cancelled because of overcrowding" />
    <Reason code="686" reasontext="This train has been cancelled because of overcrowding as this train has fewer coaches than normal" />
    <Reason code="687" reasontext="This train has been cancelled because of overcrowding because an earlier train had fewer coaches than normal" />
    <Reason code="688" reasontext="This train has been cancelled because of overcrowding because of a concert" />
    <Reason code="689" reasontext="This train has been cancelled because of overcrowding because of a football match" />
    <Reason code="690" reasontext="This train has been cancelled because of overcrowding because of a marathon" />
    <Reason code="691" reasontext="This train has been cancelled because of overcrowding because of a rugby match" />
    <Reason code="692" reasontext="This train has been cancelled because of overcrowding because of a sporting event" />
    <Reason code="693" reasontext="This train has been cancelled because of overcrowding because of an earlier cancellation" />
    <Reason code="694" reasontext="This train has been cancelled because of overcrowding because of an event" />
    <Reason code="695" reasontext="This train has been cancelled because of overcrowding earlier on this train's journey" />
    <Reason code="696" reasontext="This train has been cancelled because of animals on the railway" />
    <Reason code="697" reasontext="This train has been cancelled because of cattle on the railway" />
    <Reason code="698" reasontext="This train has been cancelled because of horses on the railway" />
    <Reason code="699" reasontext="This train has been cancelled because of sheep on the railway" />
    <Reason code="700" reasontext="This train has been cancelled because of animals on the railway earlier today" />
    <Reason code="701" reasontext="This train has been cancelled because of cattle on the railway earlier today" />
    <Reason code="702" reasontext="This train has been cancelled because of horses on the railway earlier today" />
    <Reason code="703" reasontext="This train has been cancelled because of sheep on the railway earlier today" />
    <Reason code="704" reasontext="This train has been cancelled because of passengers causing a disturbance on a train" />
    <Reason code="705" reasontext="This train has been cancelled because of passengers causing a disturbance on this train" />
    <Reason code="706" reasontext="This train has been cancelled because of passengers causing a disturbance earlier in this train's journey" />
    <Reason code="707" reasontext="This train has been cancelled because of passengers causing a disturbance on a train earlier today" />
    <Reason code="708" reasontext="This train has been cancelled because of a fault with the electric third rail earlier on this train's journey" />
    <Reason code="709" reasontext="This train has been cancelled because of a fault with the electric third rail earlier today" />
    <Reason code="710" reasontext="This train has been cancelled because of damage to the electric third rail earlier on this train's journey" />
    <Reason code="711" reasontext="This train has been cancelled because of damage to the electric third rail earlier today" />
    <Reason code="712" reasontext="This train has been cancelled because of failure of the electricity supply earlier on this train's journey" />
    <Reason code="713" reasontext="This train has been cancelled because of failure of the electricity supply earlier today" />
    <Reason code="714" reasontext="This train has been cancelled because of the electricity being switched off for safety reasons earlier on this train's journey" />
    <Reason code="715" reasontext="This train has been cancelled because of the electricity being switched off for safety reasons earlier today" />
    <Reason code="716" reasontext="This train has been cancelled because of urgent repairs to a bridge earlier today" />
    <Reason code="717" reasontext="This train has been cancelled because of urgent repairs to a tunnel earlier today" />
    <Reason code="718" reasontext="This train has been cancelled because of urgent repairs to the railway earlier today" />
    <Reason code="719" reasontext="This train has been cancelled because of urgent repairs to the track earlier today" />
    <Reason code="720" reasontext="This train has been cancelled because of expected industrial action earlier today" />
    <Reason code="721" reasontext="This train has been cancelled because of expected industrial action yesterday" />
    <Reason code="722" reasontext="This train has been cancelled because of industrial action earlier today" />
    <Reason code="723" reasontext="This train has been cancelled because of industrial action yesterday" />
    <Reason code="724" reasontext="This train has been cancelled because of an object being caught on the overhead electric wires earlier on this train's journey" />
    <Reason code="725" reasontext="This train has been cancelled because of an object being caught on the overhead electric wires earlier today" />
    <Reason code="726" reasontext="This train has been cancelled because of damage to the overhead electric wires earlier on this train's journey" />
    <Reason code="727" reasontext="This train has been cancelled because of damage to the overhead electric wires earlier today" />
    <Reason code="728" reasontext="This train has been cancelled because of earlier engineering works not being finished on time" />
    <Reason code="729" reasontext="This train has been cancelled because of a fault with the on train signalling system earlier on this train's journey" />
    <Reason code="730" reasontext="This train has been cancelled because of a fault with the on train signalling system earlier today" />
    <Reason code="733" reasontext="This train has been cancelled because of a fault with the signalling system earlier on this train's journey" />
    <Reason code="734" reasontext="This train has been cancelled because of a fault with the signalling system earlier today" />
    <Reason code="737" reasontext="This train has been cancelled because of the fire alarm sounding in a signalbox earlier on this train's journey" />
    <Reason code="738" reasontext="This train has been cancelled because of the fire alarm sounding in a signalbox earlier today" />
    <Reason code="739" reasontext="This train has been cancelled because of the fire alarm sounding in the signalling centre earlier on this train's journey" />
    <Reason code="740" reasontext="This train has been cancelled because of the fire alarm sounding in the signalling centre earlier today" />
    <Reason code="741" reasontext="This train has been cancelled because of attempted theft of overhead line electrification equipment earlier today" />
    <Reason code="742" reasontext="This train has been cancelled because of attempted theft of overhead line electrification equipment yesterday" />
    <Reason code="743" reasontext="This train has been cancelled because of attempted theft of railway equipment earlier today" />
    <Reason code="744" reasontext="This train has been cancelled because of attempted theft of railway equipment yesterday" />
    <Reason code="745" reasontext="This train has been cancelled because of attempted theft of signalling cables earlier today" />
    <Reason code="746" reasontext="This train has been cancelled because of attempted theft of signalling cables yesterday" />
    <Reason code="747" reasontext="This train has been cancelled because of attempted theft of third rail electrification equipment earlier today" />
    <Reason code="748" reasontext="This train has been cancelled because of attempted theft of third rail electrification equipment yesterday" />
    <Reason code="749" reasontext="This train has been cancelled because of theft of overhead line electrification equipment earlier today" />
    <Reason code="750" reasontext="This train has been cancelled because of theft of overhead line electrification equipment yesterday" />
    <Reason code="751" reasontext="This train has been cancelled because of theft of railway equipment earlier today" />
    <Reason code="752" reasontext="This train has been cancelled because of theft of railway equipment yesterday" />
    <Reason code="753" reasontext="This train has been cancelled because of theft of signalling cables earlier today" />
    <Reason code="754" reasontext="This train has been cancelled because of theft of signalling cables yesterday" />
    <Reason code="755" reasontext="This train has been cancelled because of theft of third rail electrification equipment earlier today" />
    <Reason code="756" reasontext="This train has been cancelled because of theft of third rail electrification equipment yesterday" />
    <Reason code="757" reasontext="This train has been cancelled because of vandalism at a station earlier today" />
    <Reason code="758" reasontext="This train has been cancelled because of vandalism at a station yesterday" />
    <Reason code="759" reasontext="This train has been cancelled because of vandalism of railway equipment earlier today" />
    <Reason code="760" reasontext="This train has been cancelled because of vandalism of railway equipment yesterday" />
    <Reason code="761" reasontext="This train has been cancelled because of vandalism on a train earlier today" />
    <Reason code="762" reasontext="This train has been cancelled because of vandalism on a train yesterday" />
    <Reason code="763" reasontext="This train has been cancelled because of vandalism on this train earlier today" />
    <Reason code="764" reasontext="This train has been cancelled because of vandalism on this train yesterday" />
    <Reason code="765" reasontext="This train has been cancelled because of a fault with the electric third rail" />
    <Reason code="766" reasontext="This train has been cancelled because of damage to the electric third rail" />
    <Reason code="767" reasontext="This train has been cancelled because of failure of the electricity supply" />
    <Reason code="768" reasontext="This train has been cancelled because of the electricity being switched off for safety reasons" />
    <Reason code="769" reasontext="This train has been cancelled because of urgent repairs to a bridge" />
    <Reason code="770" reasontext="This train has been cancelled because of urgent repairs to a tunnel" />
    <Reason code="771" reasontext="This train has been cancelled because of urgent repairs to the railway" />
    <Reason code="772" reasontext="This train has been cancelled because of urgent repairs to the track" />
    <Reason code="773" reasontext="This train has been cancelled because of the emergency services dealing with an incident earlier today" />
    <Reason code="774" reasontext="This train has been cancelled because of ambulance service dealing with an incident" />
    <Reason code="775" reasontext="This train has been cancelled because of fire brigade dealing with an incident" />
    <Reason code="776" reasontext="This train has been cancelled because of police dealing with an incident" />
    <Reason code="777" reasontext="This train has been cancelled because of the emergency services dealing with an incident" />
    <Reason code="778" reasontext="This train has been cancelled because of the fire alarm sounding at a station" />
    <Reason code="779" reasontext="This train has been cancelled because of the fire alarm sounding at a station earlier today" />
    <Reason code="780" reasontext="This train has been cancelled because of a burst water main flooding the railway" />
    <Reason code="781" reasontext="This train has been cancelled because of a river flooding the railway" />
    <Reason code="782" reasontext="This train has been cancelled because of flood water making the railway potentially unsafe" />
    <Reason code="783" reasontext="This train has been cancelled because of flooding" />
    <Reason code="784" reasontext="This train has been cancelled because of heavy rain flooding the railway" />
    <Reason code="785" reasontext="This train has been cancelled because of predicted flooding" />
    <Reason code="786" reasontext="This train has been cancelled because of the sea flooding the railway" />
    <Reason code="787" reasontext="This train has been cancelled because of a burst water main flooding the railway earlier today" />
    <Reason code="788" reasontext="This train has been cancelled because of a river flooding the railway earlier today" />
    <Reason code="789" reasontext="This train has been cancelled because of flood water making the railway potentially unsafe earlier today" />
    <Reason code="790" reasontext="This train has been cancelled because of flooding earlier in this train's journey" />
    <Reason code="791" reasontext="This train has been cancelled because of flooding earlier today" />
    <Reason code="792" reasontext="This train has been cancelled because of heavy rain flooding the railway earlier today" />
    <Reason code="793" reasontext="This train has been cancelled because of predicted flooding earlier today" />
    <Reason code="794" reasontext="This train has been cancelled because of the sea flooding the railway earlier today" />
    <Reason code="795" reasontext="This train has been cancelled because of thick fog" />
    <Reason code="796" reasontext="This train has been cancelled because of thick fog earlier in this train's journey" />
    <Reason code="797" reasontext="This train has been cancelled because of thick fog earlier today" />
    <Reason code="798" reasontext="This train has been cancelled because of forecasted high winds" />
    <Reason code="799" reasontext="This train has been cancelled because of high winds" />
    <Reason code="800" reasontext="This train has been cancelled because of high winds earlier in this train's journey" />
    <Reason code="801" reasontext="This train has been cancelled because of high winds earlier today" />
    <Reason code="802" reasontext="This train has been cancelled because of expected industrial action" />
    <Reason code="803" reasontext="This train has been cancelled because of industrial action" />
    <Reason code="804" reasontext="This train has been cancelled because of lightning damaging a station" />
    <Reason code="805" reasontext="This train has been cancelled because of lightning damaging a train" />
    <Reason code="806" reasontext="This train has been cancelled because of lightning damaging equipment" />
    <Reason code="807" reasontext="This train has been cancelled because of lightning damaging the electricity supply" />
    <Reason code="808" reasontext="This train has been cancelled because of lightning damaging the signalling system" />
    <Reason code="809" reasontext="This train has been cancelled because of lightning damaging this train" />
    <Reason code="810" reasontext="This train has been cancelled because of an object being caught on the overhead electric wires" />
    <Reason code="811" reasontext="This train has been cancelled because of damage to the overhead electric wires" />
    <Reason code="812" reasontext="This train has been cancelled because of engineering works not being finished on time" />
    <Reason code="813" reasontext="This train has been cancelled because of forecasted slippery rails" />
    <Reason code="814" reasontext="This train has been cancelled because of ice preventing this train getting electricity from the third rail" />
    <Reason code="815" reasontext="This train has been cancelled because of ice preventing trains getting electricity from the third rail" />
    <Reason code="816" reasontext="This train has been cancelled because of slippery rails" />
    <Reason code="817" reasontext="This train has been cancelled because of slippery rails earlier in this train's journey" />
    <Reason code="818" reasontext="This train has been cancelled because of slippery rails earlier today" />
    <Reason code="819" reasontext="This train has been cancelled because of forecasted severe weather" />
    <Reason code="820" reasontext="This train has been cancelled because of severe weather" />
    <Reason code="821" reasontext="This train has been cancelled because of severe weather earlier" />
    <Reason code="822" reasontext="This train has been cancelled because of severe weather earlier in this train's journey" />
    <Reason code="823" reasontext="This train has been cancelled because of severe weather earlier today" />
    <Reason code="824" reasontext="This train has been cancelled because of a safety inspection of the track" />
    <Reason code="825" reasontext="This train has been cancelled because of a safety inspection on a train" />
    <Reason code="826" reasontext="This train has been cancelled because of a safety inspection on this train" />
    <Reason code="827" reasontext="This train has been cancelled because of a safety inspection of the track earlier today" />
    <Reason code="828" reasontext="This train has been cancelled because of a safety inspection on a train earlier today" />
    <Reason code="829" reasontext="This train has been cancelled because of a safety inspection on this train earlier in its journey" />
    <Reason code="830" reasontext="This train has been cancelled because of a fault with the on train signalling system" />
    <Reason code="831" reasontext="This train has been cancelled because of a fault with the radio system between the driver and the signaller" />
    <Reason code="832" reasontext="This train has been cancelled because of a fault with the signalling system" />
    <Reason code="834" reasontext="This train has been cancelled because of the fire alarm sounding in a signalbox" />
    <Reason code="835" reasontext="This train has been cancelled because of the fire alarm sounding in the signalling centre" />
    <Reason code="836" reasontext="This train has been cancelled because of forecasted heavy snow" />
    <Reason code="837" reasontext="This train has been cancelled because of heavy snow" />
    <Reason code="838" reasontext="This train has been cancelled because of heavy snow earlier in this train's journey" />
    <Reason code="839" reasontext="This train has been cancelled because of heavy snow earlier today" />
    <Reason code="840" reasontext="This train has been cancelled because of heavy snow over recent days" />
    <Reason code="841" reasontext="This train has been cancelled because of a speed restriction" />
    <Reason code="842" reasontext="This train has been cancelled because of a speed restriction because of fog" />
    <Reason code="843" reasontext="This train has been cancelled because of a speed restriction because of fog earlier on this train's journey" />
    <Reason code="844" reasontext="This train has been cancelled because of a speed restriction because of fog earlier today" />
    <Reason code="845" reasontext="This train has been cancelled because of a speed restriction because of heavy rain" />
    <Reason code="846" reasontext="This train has been cancelled because of a speed restriction because of heavy rain earlier on this train's journey" />
    <Reason code="847" reasontext="This train has been cancelled because of a speed restriction because of heavy rain earlier today" />
    <Reason code="848" reasontext="This train has been cancelled because of a speed restriction because of high track temperatures" />
    <Reason code="849" reasontext="This train has been cancelled because of a speed restriction because of high track temperatures earlier on this train's journey" />
    <Reason code="850" reasontext="This train has been cancelled because of a speed restriction because of high track temperatures earlier today" />
    <Reason code="851" reasontext="This train has been cancelled because of a speed restriction because of high winds" />
    <Reason code="852" reasontext="This train has been cancelled because of a speed restriction because of high winds earlier on this train's journey" />
    <Reason code="853" reasontext="This train has been cancelled because of a speed restriction because of high winds earlier today" />
    <Reason code="854" reasontext="This train has been cancelled because of a speed restriction because of severe weather" />
    <Reason code="855" reasontext="This train has been cancelled because of a speed restriction because of severe weather earlier on this train's journey" />
    <Reason code="856" reasontext="This train has been cancelled because of a speed restriction because of severe weather earlier today" />
    <Reason code="857" reasontext="This train has been cancelled because of a speed restriction because of snow and ice" />
    <Reason code="858" reasontext="This train has been cancelled because of a speed restriction because of snow and ice earlier on this train's journey" />
    <Reason code="859" reasontext="This train has been cancelled because of a speed restriction because of snow and ice earlier today" />
    <Reason code="860" reasontext="This train has been cancelled because of a speed restriction earlier on this train's journey" />
    <Reason code="861" reasontext="This train has been cancelled because of a speed restriction earlier today" />
    <Reason code="862" reasontext="This train has been cancelled because of a speed restriction in a tunnel" />
    <Reason code="863" reasontext="This train has been cancelled because of a speed restriction in a tunnel earlier on this train's journey" />
    <Reason code="864" reasontext="This train has been cancelled because of a speed restriction in a tunnel earlier today" />
    <Reason code="865" reasontext="This train has been cancelled because of a speed restriction over a bridge" />
    <Reason code="866" reasontext="This train has been cancelled because of a speed restriction over a bridge earlier on this train's journey" />
    <Reason code="867" reasontext="This train has been cancelled because of a speed restriction over a bridge earlier today" />
    <Reason code="868" reasontext="This train has been cancelled because of a speed restriction over an embankment" />
    <Reason code="869" reasontext="This train has been cancelled because of a speed restriction over an embankment earlier on this train's journey" />
    <Reason code="870" reasontext="This train has been cancelled because of a speed restriction over an embankment earlier today" />
    <Reason code="871" reasontext="This train has been cancelled because of a speed restriction over defective track" />
    <Reason code="872" reasontext="This train has been cancelled because of a speed restriction over defective track earlier on this train's journey" />
    <Reason code="873" reasontext="This train has been cancelled because of a speed restriction over defective track earlier today" />
    <Reason code="874" reasontext="This train has been cancelled because of attempted theft of overhead line electrification equipment" />
    <Reason code="875" reasontext="This train has been cancelled because of attempted theft of railway equipment" />
    <Reason code="876" reasontext="This train has been cancelled because of attempted theft of signalling cables" />
    <Reason code="877" reasontext="This train has been cancelled because of attempted theft of third rail electrification equipment" />
    <Reason code="878" reasontext="This train has been cancelled because of theft of overhead line electrification equipment" />
    <Reason code="879" reasontext="This train has been cancelled because of theft of railway equipment" />
    <Reason code="880" reasontext="This train has been cancelled because of theft of signalling cables" />
    <Reason code="881" reasontext="This train has been cancelled because of theft of third rail electrification equipment" />
    <Reason code="882" reasontext="This train has been cancelled because of vandalism at a station" />
    <Reason code="883" reasontext="This train has been cancelled because of vandalism of railway equipment" />
    <Reason code="884" reasontext="This train has been cancelled because of vandalism on a train" />
    <Reason code="885" reasontext="This train has been cancelled because of vandalism on this train" />
    <Reason code="886" reasontext="This train has been cancelled because of train crew being delayed" />
    <Reason code="887" reasontext="This train has been cancelled because of train crew being delayed by service disruption" />
    <Reason code="888" reasontext="This train has been cancelled because of a bridge being damaged" />
    <Reason code="889" reasontext="This train has been cancelled because of a bridge being damaged by a boat" />
    <Reason code="890" reasontext="This train has been cancelled because of a bridge being damaged by a road vehicle" />
    <Reason code="891" reasontext="This train has been cancelled because of a bridge having collapsed" />
    <Reason code="892" reasontext="This train has been cancelled because of a broken rail" />
    <Reason code="893" reasontext="This train has been cancelled because of a late departure while the train was cleaned specially" />
    <Reason code="894" reasontext="This train has been cancelled because of a late running freight train" />
    <Reason code="895" reasontext="This train has been cancelled because of a late running train being in front of this one" />
    <Reason code="896" reasontext="This train has been cancelled because of a points failure" />
    <Reason code="897" reasontext="This train has been cancelled because of a power cut at the station" />
    <Reason code="898" reasontext="This train has been cancelled because of a problem with the station lighting" />
    <Reason code="899" reasontext="This train has been cancelled because of a rail buckling in the heat" />
    <Reason code="900" reasontext="This train has been cancelled because of a railway embankment being damaged" />
    <Reason code="901" reasontext="This train has been cancelled because of a shortage of station staff" />
    <Reason code="902" reasontext="This train has been cancelled because of a tunnel being closed for safety reasons" />
    <Reason code="903" reasontext="This train has been cancelled because of an incident at the airport" />
    <Reason code="904" reasontext="This train has been cancelled because of congestion" />
    <Reason code="905" reasontext="This train has been cancelled because of the communication alarm being activated on a train" />
    <Reason code="906" reasontext="This train has been cancelled because of the communication alarm being activated on this train" />
    <Reason code="907" reasontext="This train has been cancelled because of the train departing late to maintain customer connections" />
    <Reason code="908" reasontext="This train has been cancelled because of the train making extra stops because a train was cancelled" />
    <Reason code="909" reasontext="This train has been cancelled because of the train making extra stops because of service disruption" />
    <Reason code="910" reasontext="This train has been cancelled because of waiting for a part of the train to be attached" />
    <Reason code="911" reasontext="This train has been cancelled because of a fault on a train" />
    <Reason code="912" reasontext="This train has been cancelled because of a problem with platform equipment" />
    <Reason code="913" reasontext="This train has been cancelled because of a fault on a train earlier" />
    <Reason code="914" reasontext="This train has been cancelled because of issues with communication systems" />
    <Reason code="915" reasontext="This train has been cancelled because of a problem in the depot" />
    <Reason code="916" reasontext="This train has been cancelled because of signalling staff being unavailable" />
    <Reason code="917" reasontext="This train has been cancelled because of staff training" />
    <Reason code="918" reasontext="This train has been cancelled because of a short-notice change to the timetable" />
    <Reason code="919" reasontext="This train has been cancelled because of misuse of a level crossing" />
    <Reason code="920" reasontext="This train has been cancelled because of a member of train crew being unavailable" />
    <Reason code="921" reasontext="This train has been cancelled because of a member of train crew being unavailable earlier" />
  </CancellationReasons>
  </xml>
`
