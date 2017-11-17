package notifiers

import (
	"regexp"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/log"
	m "github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/alerting"
)

func init() {
	alerting.RegisterNotifier(&alerting.NotifierPlugin{
		Type:        "alertmanager",
		Name:        "alertmanager",
		Description: "Sends alert to Alertmanager",
		Factory:     NewAlertmanagerNotifier,
		OptionsTemplate: `
      <h3 class="page-heading">Alertmanager settings</h3>
      <div class="gf-form">
        <span class="gf-form-label width-10">Url</span>
        <input type="text" required class="gf-form-input max-width-26" ng-model="ctrl.model.settings.url" placeholder="http://localhost:9093"></input>
      </div>
    `,
	})
}

func NewAlertmanagerNotifier(model *m.AlertNotification) (alerting.Notifier, error) {
	url := model.Settings.Get("url").MustString()
	if url == "" {
		return nil, alerting.ValidationError{Reason: "Could not find url property in settings"}
	}

	return &AlertmanagerNotifier{
		NotifierBase: NewNotifierBase(model.Id, model.IsDefault, model.Name, model.Type, model.Settings),
		Url:          url,
		log:          log.New("alerting.notifier.alertmanager"),
	}, nil
}

type AlertmanagerNotifier struct {
	NotifierBase
	Url string
	log log.Logger
}

func (this *AlertmanagerNotifier) ShouldNotify(evalContext *alerting.EvalContext) bool {
	return true
}

func (this *AlertmanagerNotifier) Notify(evalContext *alerting.EvalContext) error {
	this.log.Info("Sending alertmanager")

	// We can't define an alert per evalMatch since we wouldn't be able to send resolve.
	// Indeed evalContext.evalMatches is empty when state is OK.
	// Therefore we define only one alert for the evalContext.Rule
	alertJSON := simplejson.New()
	alertJSON.Set("startsAt", evalContext.StartTime.UTC().Format(time.RFC3339))
	if evalContext.Rule.State == m.AlertStateAlerting {
		alertJSON.Set("endsAt", "0001-01-01T00:00:00Z")
	} else {
		alertJSON.Set("endsAt", evalContext.EndTime.UTC().Format(time.RFC3339))
	}

	ruleURL, err := evalContext.GetRuleUrl()
	if err == nil {
		alertJSON.Set("generatorURL", ruleURL)
	}

	alertJSON.Set("annotations", parseAnnotations(evalContext))
	alertJSON.Set("labels", parseLabels(evalContext.Rule))

	// Alertmanager requires a JsonArray
	bodyJSON := simplejson.NewFromAny([]*simplejson.Json{alertJSON})
	body, _ := bodyJSON.MarshalJSON()

	cmd := &m.SendWebhookSync{
		Url:        this.Url + "/api/v1/alerts",
		HttpMethod: "POST",
		Body:       string(body),
	}

	if err := bus.DispatchCtx(evalContext.Ctx, cmd); err != nil {
		this.log.Error("Failed to send alertmanager", "error", err, "alertmanager", this.Name)
		return err
	}

	return nil
}

func parseAnnotations(evalContext *alerting.EvalContext) map[string]string {
	annotations := make(map[string]string)

	if evalContext.Rule.Message != "" {
		annotations["description"] = evalContext.Rule.Message
	}

	formattedMatches := ""
	for _, evalMatch := range evalContext.EvalMatches {
		formattedMatches = formattedMatches + evalMatch.Metric + " : " + evalMatch.Value.String() + "\n"
	}
	if formattedMatches != "" {
		annotations["evalMatches"] = formattedMatches
	}

	return annotations
}

func parseLabels(rule *alerting.Rule) map[string]string {
	labels := make(map[string]string)
	labels["alertname"] = rule.Name

	if rule.Message != "" {
		re := regexp.MustCompile("\"(.+)\":\"(.+)\"")
		for _, line := range strings.Split(rule.Message, "\n") {
			match := re.FindAllStringSubmatch(line, 1)
			if match != nil {
				labelName := match[0][1]
				labelValue := match[0][2]
				labels[labelName] = labelValue
			}
		}
	}
	return labels
}
