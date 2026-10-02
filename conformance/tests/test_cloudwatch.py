"""CloudWatch metrics and alarms through boto3.

Cost on AWS: a custom metric and an alarm are each billed by the month,
prorated by the hour, and each one here lives for a couple of minutes.

A datapoint can take a minute or two to become readable on AWS, so the reads
poll; against doze-aws they return at once.
"""

import datetime

# When an alarm was last touched, and its ARN's dependence on nothing but names.
CLOCK = ("AlarmConfigurationUpdatedTimestamp", "StateUpdatedTimestamp", "StateTransitionedTimestamp",
         "Timestamp")


def now():
    return datetime.datetime.now(datetime.timezone.utc)


def test_metric_data_reads_back_as_statistics(client, names, cleanup, snapshot, eventually):
    cw = client("cloudwatch")
    ns = f"dzc/{names('ns')}"
    dims = [{"Name": "queue", "Value": "orders"}]
    at = now().replace(second=0, microsecond=0) - datetime.timedelta(minutes=2)
    snapshot.match("put", cw.put_metric_data(Namespace=ns, MetricData=[
        {"MetricName": "depth", "Dimensions": dims, "Timestamp": at, "Value": v, "Unit": "Count"}
        for v in (1.0, 2.0, 6.0)]))
    cw.put_metric_data(Namespace=ns, MetricData=[{
        "MetricName": "latency", "Timestamp": at, "Unit": "Milliseconds",
        "StatisticValues": {"SampleCount": 4, "Sum": 100, "Minimum": 10, "Maximum": 40}}])

    window = dict(StartTime=at - datetime.timedelta(minutes=5),
                  EndTime=at + datetime.timedelta(minutes=5), Period=60)

    def stats(metric, dimensions=()):
        def read():
            r = cw.get_metric_statistics(
                Namespace=ns, MetricName=metric, Dimensions=list(dimensions),
                Statistics=["SampleCount", "Sum", "Average", "Minimum", "Maximum"], **window)
            assert r["Datapoints"], "no datapoints yet"
            return r
        return eventually(read, timeout=240, every=5)

    snapshot.match("statistics", stats("depth", dims), opaque=CLOCK)
    snapshot.match("statistic-set", stats("latency"), opaque=CLOCK)
    # Dimensions are part of a metric's identity: no dimensions is another metric.
    snapshot.match("without-its-dimensions", cw.get_metric_statistics(
        Namespace=ns, MetricName="depth", Statistics=["Sum"], **window))

    def listed():
        r = cw.list_metrics(Namespace=ns)
        assert len(r["Metrics"]) >= 2, f"{len(r['Metrics'])} of 2 listed so far"
        return r

    snapshot.match("list", eventually(listed, timeout=900, every=10), unordered=("Metrics",))
    snapshot.match("get-metric-data", cw.get_metric_data(
        MetricDataQueries=[{"Id": "d", "MetricStat": {
            "Metric": {"Namespace": ns, "MetricName": "depth", "Dimensions": dims},
            "Period": 60, "Stat": "Sum"}}],
        StartTime=window["StartTime"], EndTime=window["EndTime"]), opaque=("Timestamps",))


def test_alarm_lifecycle(client, names, cleanup, snapshot):
    cw = client("cloudwatch")
    name = names("alarm")
    snapshot.match("put", cw.put_metric_alarm(
        AlarmName=name, AlarmDescription="queue is backing up",
        Namespace=f"dzc/{names('ns')}", MetricName="depth", Statistic="Maximum",
        Dimensions=[{"Name": "queue", "Value": "orders"}],
        Period=60, EvaluationPeriods=2, DatapointsToAlarm=2, Threshold=100,
        ComparisonOperator="GreaterThanThreshold", TreatMissingData="notBreaching",
        ActionsEnabled=False))
    cleanup(cw.delete_alarms, AlarmNames=[name])

    snapshot.match("describe", cw.describe_alarms(AlarmNames=[name]), opaque=CLOCK,
                   drop=("StateValue", "StateReason", "StateReasonData"))
    snapshot.match("set-state", cw.set_alarm_state(
        AlarmName=name, StateValue="ALARM", StateReason="forced by the scenario"))
    snapshot.match("in-alarm", [(a["StateValue"], a["StateReason"]) for a in cw.describe_alarms(
        AlarmNames=[name])["MetricAlarms"]])
    snapshot.match("disable-actions", cw.disable_alarm_actions(AlarmNames=[name]))
    snapshot.match("tag", cw.tag_resource(
        ResourceARN=cw.describe_alarms(AlarmNames=[name])["MetricAlarms"][0]["AlarmArn"],
        Tags=[{"Key": "env", "Value": "dev"}]))
    snapshot.match("by-prefix", [a["AlarmName"] for a in cw.describe_alarms(
        AlarmNamePrefix=name)["MetricAlarms"]])
    snapshot.match("delete", cw.delete_alarms(AlarmNames=[name]))
    snapshot.match("gone", cw.describe_alarms(AlarmNames=[name]))
    snapshot.outcome("delete-again", lambda: cw.delete_alarms(AlarmNames=[name]))


def test_refusals(client, names, cleanup, snapshot):
    cw = client("cloudwatch")
    ns, name = f"dzc/{names('ns')}", names("alarm")
    cleanup(cw.delete_alarms, AlarmNames=[name])
    alarm = dict(AlarmName=name, Namespace=ns, MetricName="m", Statistic="Sum", Period=60,
                 EvaluationPeriods=1, Threshold=1, ComparisonOperator="GreaterThanThreshold")

    snapshot.error("reserved-namespace", lambda: cw.put_metric_data(
        Namespace="AWS/Mine", MetricData=[{"MetricName": "m", "Value": 1}]))
    snapshot.error("value-and-statistics", lambda: cw.put_metric_data(Namespace=ns, MetricData=[{
        "MetricName": "m", "Value": 1,
        "StatisticValues": {"SampleCount": 1, "Sum": 1, "Minimum": 1, "Maximum": 1}}]))
    snapshot.error("bad-unit", lambda: cw.put_metric_data(
        Namespace=ns, MetricData=[{"MetricName": "m", "Value": 1, "Unit": "Furlongs"}]))
    snapshot.error("period-not-a-minute", lambda: cw.get_metric_statistics(
        Namespace=ns, MetricName="m", Statistics=["Sum"], Period=45,
        StartTime=now() - datetime.timedelta(hours=1), EndTime=now()))
    snapshot.error("end-before-start", lambda: cw.get_metric_statistics(
        Namespace=ns, MetricName="m", Statistics=["Sum"], Period=60,
        StartTime=now(), EndTime=now() - datetime.timedelta(hours=1)))
    snapshot.error("no-statistic", lambda: cw.get_metric_statistics(
        Namespace=ns, MetricName="m", Period=60,
        StartTime=now() - datetime.timedelta(hours=1), EndTime=now()))

    snapshot.error("bad-comparison", lambda: cw.put_metric_alarm(
        **{**alarm, "ComparisonOperator": "RoughlyEqualTo"}))
    snapshot.error("more-datapoints-than-periods", lambda: cw.put_metric_alarm(
        **alarm, DatapointsToAlarm=3))
    snapshot.error("statistic-and-extended", lambda: cw.put_metric_alarm(
        **alarm, ExtendedStatistic="p99"))
    snapshot.error("bad-treat-missing", lambda: cw.put_metric_alarm(
        **alarm, TreatMissingData="shrug"))
    snapshot.error("set-state-of-absent-alarm", lambda: cw.set_alarm_state(
        AlarmName=names("absent"), StateValue="OK", StateReason="x"))
    snapshot.error("bad-state", lambda: cw.set_alarm_state(
        AlarmName=name, StateValue="PANIC", StateReason="x"))
