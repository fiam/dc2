#!/usr/bin/env bash
set -euo pipefail

# Example flow:
# 1. Build an nginx image that self-configures from IMDS on startup.
# 2. Create a launch template for that image.
# 3. Create an Auto Scaling Group from that launch template.
# 4. Wait for instances to reach InService.
# 5. Pick a random instance and curl it from a curlimages container.
# 6. Optionally fail an instance Docker healthcheck and verify replacement.
#
# Prerequisite: dc2 must already be running and reachable at DC2_ENDPOINT.
# Build and run dc2 locally from source with one of these options:
#
# Host process:
#   INSTANCE_NETWORK=dc2-workload go run ./cmd/dc2 --addr 0.0.0.0:8080
#
# Container (local image build):
#   make image
#   docker run --rm --name dc2 \
#     -p 8080:8080 \
#     -e ADDR=0.0.0.0:8080 \
#     -e INSTANCE_NETWORK=dc2-workload \
#     -v /var/run/docker.sock:/var/run/docker.sock \
#     dc2
#
# Then run:
#   WORKLOAD_NETWORK=dc2-workload ./examples/asg-curl.sh

DC2_ENDPOINT="${DC2_ENDPOINT:-http://localhost:8080}"
AWS_REGION="${AWS_REGION:-us-east-1}"
AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-test}"
AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-test}"
AWS_SESSION_TOKEN="${AWS_SESSION_TOKEN:-}"
AWS_PAGER="${AWS_PAGER:-}"

DESIRED_CAPACITY="${DESIRED_CAPACITY:-2}"
MIN_SIZE="${MIN_SIZE:-${DESIRED_CAPACITY}}"
MAX_SIZE="${MAX_SIZE:-${DESIRED_CAPACITY}}"

INSTANCE_IMAGE="${INSTANCE_IMAGE:-dc2-example-nginx-imds:local}"
INSTANCE_TYPE="${INSTANCE_TYPE:-a1.large}"
CURL_IMAGE="${CURL_IMAGE:-curlimages/curl:8.12.1}"
WORKLOAD_NETWORK="${WORKLOAD_NETWORK:-${INSTANCE_NETWORK:-bridge}}"
BUILD_INSTANCE_IMAGE="${BUILD_INSTANCE_IMAGE:-1}"

WAIT_TIMEOUT_SECONDS="${WAIT_TIMEOUT_SECONDS:-60}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-1}"
KEEP_RESOURCES="${KEEP_RESOURCES:-0}"
TEST_HEALTHCHECK_REPLACEMENT="${TEST_HEALTHCHECK_REPLACEMENT:-0}"

export AWS_REGION AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_PAGER

if ! command -v aws >/dev/null 2>&1; then
  echo "error: aws CLI is required" >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is required" >&2
  exit 1
fi

if ! docker network inspect "${WORKLOAD_NETWORK}" >/dev/null 2>&1; then
  echo "error: docker network ${WORKLOAD_NETWORK} was not found" >&2
  exit 1
fi

if [[ ! "${DESIRED_CAPACITY}" =~ ^[0-9]+$ ]] || [[ ! "${MIN_SIZE}" =~ ^[0-9]+$ ]] || [[ ! "${MAX_SIZE}" =~ ^[0-9]+$ ]]; then
  echo "error: DESIRED_CAPACITY, MIN_SIZE, and MAX_SIZE must be integers" >&2
  exit 1
fi

if (( MIN_SIZE > DESIRED_CAPACITY )) || (( DESIRED_CAPACITY > MAX_SIZE )); then
  echo "error: expected MIN_SIZE <= DESIRED_CAPACITY <= MAX_SIZE" >&2
  exit 1
fi

if [[ ! "${WAIT_TIMEOUT_SECONDS}" =~ ^[0-9]+$ ]] || [[ ! "${POLL_INTERVAL_SECONDS}" =~ ^[0-9]+$ ]] || (( POLL_INTERVAL_SECONDS == 0 )); then
  echo "error: WAIT_TIMEOUT_SECONDS must be an integer and POLL_INTERVAL_SECONDS must be a non-zero integer" >&2
  exit 1
fi

build_instance_image() {
  if [[ "${BUILD_INSTANCE_IMAGE}" != "1" ]]; then
    if ! docker image inspect "${INSTANCE_IMAGE}" >/dev/null 2>&1; then
      echo "error: image ${INSTANCE_IMAGE} not found and BUILD_INSTANCE_IMAGE!=1" >&2
      exit 1
    fi
    return
  fi

  tmp_dir="$(mktemp -d)"

  cat >"${tmp_dir}/Dockerfile" <<'EOF'
FROM nginx:alpine

RUN apk add --no-cache curl
COPY 10-imds-instance-id.sh /docker-entrypoint.d/10-imds-instance-id.sh
RUN chmod +x /docker-entrypoint.d/10-imds-instance-id.sh && touch /tmp/dc2-health
HEALTHCHECK --interval=1s --timeout=1s --retries=2 CMD test -f /tmp/dc2-health
EOF

  cat >"${tmp_dir}/10-imds-instance-id.sh" <<'EOF'
#!/bin/sh
set -eu

token=""
for _ in 1 2 3 4 5 6 7 8 9 10; do
  token="$(curl -fsS -X PUT -H "X-aws-ec2-metadata-token-ttl-seconds: 60" http://169.254.169.254/latest/api/token || true)"
  if [ -n "$token" ]; then
    break
  fi
  sleep 1
done
if [ -z "$token" ]; then
  echo "warning: failed to fetch IMDS token" >&2
  exit 0
fi

instance_id=""
for _ in 1 2 3 4 5 6 7 8 9 10; do
  instance_id="$(curl -fsS -H "X-aws-ec2-metadata-token: ${token}" http://169.254.169.254/latest/meta-data/instance-id || true)"
  if [ -n "$instance_id" ]; then
    break
  fi
  sleep 1
done
if [ -z "$instance_id" ]; then
  echo "warning: failed to fetch IMDS instance-id" >&2
  exit 0
fi

printf "Hello from %s\n" "$instance_id" > /usr/share/nginx/html/index.html
EOF

  echo "building self-configuring instance image ${INSTANCE_IMAGE}"
  if ! docker build -t "${INSTANCE_IMAGE}" "${tmp_dir}"; then
    rm -rf "${tmp_dir}"
    exit 1
  fi
  rm -rf "${tmp_dir}"
}

suffix="$(date +%s)-${RANDOM}"
launch_template_name="lt-nginx-${suffix}"
auto_scaling_group_name="asg-nginx-${suffix}"

build_instance_image

cleanup() {
  if [[ "${KEEP_RESOURCES}" == "1" ]]; then
    echo "keeping resources: launch_template=${launch_template_name} asg=${auto_scaling_group_name}"
    return
  fi

  echo "cleaning up Auto Scaling Group ${auto_scaling_group_name}"
  aws autoscaling delete-auto-scaling-group \
    --endpoint-url "${DC2_ENDPOINT}" \
    --auto-scaling-group-name "${auto_scaling_group_name}" \
    --force-delete >/dev/null 2>&1 || true

  deletion_deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  while (( SECONDS < deletion_deadline )); do
    group_count="$(
      aws autoscaling describe-auto-scaling-groups \
        --endpoint-url "${DC2_ENDPOINT}" \
        --auto-scaling-group-names "${auto_scaling_group_name}" \
        --query 'length(AutoScalingGroups)' \
        --output text 2>/dev/null || true
    )"
    if [[ "${group_count}" == "0" || "${group_count}" == "None" ]]; then
      break
    fi
    sleep "${POLL_INTERVAL_SECONDS}"
  done

  echo "cleaning up launch template ${launch_template_name}"
  aws ec2 delete-launch-template \
    --endpoint-url "${DC2_ENDPOINT}" \
    --launch-template-name "${launch_template_name}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

contains_instance_id() {
  local needle="${1}"
  shift
  local value
  for value in "$@"; do
    if [[ "${value}" == "${needle}" ]]; then
      return 0
    fi
  done
  return 1
}

wait_for_inservice_instances() {
  local expected_capacity="${1}"
  local deadline_local=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  instance_ids=()

  while (( SECONDS < deadline_local )); do
    raw_ids="$(
      aws autoscaling describe-auto-scaling-groups \
        --endpoint-url "${DC2_ENDPOINT}" \
        --auto-scaling-group-names "${auto_scaling_group_name}" \
        --query "AutoScalingGroups[0].Instances[?LifecycleState=='InService'].InstanceId" \
        --output text
    )"

    instance_ids=()
    if [[ -n "${raw_ids}" && "${raw_ids}" != "None" ]]; then
      read -r -a instance_ids <<<"${raw_ids}"
    fi

    if (( ${#instance_ids[@]} >= expected_capacity )); then
      return 0
    fi

    sleep "${POLL_INTERVAL_SECONDS}"
  done

  return 1
}

curl_instance() {
  local instance_id="${1}"
  local private_ip
  local public_ip
  private_ip="$(
    aws ec2 describe-instances \
      --endpoint-url "${DC2_ENDPOINT}" \
      --instance-ids "${instance_id}" \
      --query 'Reservations[0].Instances[0].PrivateIpAddress' \
      --output text
  )"
  public_ip="$(
    aws ec2 describe-instances \
      --endpoint-url "${DC2_ENDPOINT}" \
      --instance-ids "${instance_id}" \
      --query 'Reservations[0].Instances[0].PublicIpAddress' \
      --output text
  )"

  if [[ -z "${private_ip}" || "${private_ip}" == "None" ]]; then
    echo "error: instance ${instance_id} has no reachable IP in DescribeInstances output" >&2
    exit 1
  fi

  echo "selected instance=${instance_id} private_ip=${private_ip} public_ip=${public_ip}"
  echo "running curl container on network=${WORKLOAD_NETWORK}"
  docker run --rm \
    --network "${WORKLOAD_NETWORK}" \
    "${CURL_IMAGE}" \
    -v \
    --retry 5 \
    --retry-delay 1 \
    --retry-connrefused \
    -fS \
    "http://${private_ip}"
}

echo "creating launch template ${launch_template_name}"
aws ec2 create-launch-template \
  --endpoint-url "${DC2_ENDPOINT}" \
  --launch-template-name "${launch_template_name}" \
  --launch-template-data "{\"ImageId\":\"${INSTANCE_IMAGE}\",\"InstanceType\":\"${INSTANCE_TYPE}\"}" \
  --query 'LaunchTemplate.LaunchTemplateId' \
  --output text >/dev/null

echo "creating Auto Scaling Group ${auto_scaling_group_name} with desired=${DESIRED_CAPACITY}"
aws autoscaling create-auto-scaling-group \
  --endpoint-url "${DC2_ENDPOINT}" \
  --auto-scaling-group-name "${auto_scaling_group_name}" \
  --min-size "${MIN_SIZE}" \
  --max-size "${MAX_SIZE}" \
  --desired-capacity "${DESIRED_CAPACITY}" \
  --launch-template "LaunchTemplateName=${launch_template_name},Version=\$Default"

echo "waiting for ${DESIRED_CAPACITY} InService instance(s)"
declare -a instance_ids=()
if ! wait_for_inservice_instances "${DESIRED_CAPACITY}"; then
  echo "error: timed out waiting for InService instances in ${auto_scaling_group_name}" >&2
  exit 1
fi

random_index=$((RANDOM % ${#instance_ids[@]}))
instance_id="${instance_ids[${random_index}]}"
initial_instance_ids=("${instance_ids[@]}")
curl_instance "${instance_id}"

if [[ "${TEST_HEALTHCHECK_REPLACEMENT}" == "1" ]]; then
  unhealthy_instance_id="${instance_id}"
  unhealthy_container_id="${unhealthy_instance_id#i-}"

  echo "forcing healthcheck failure for instance ${unhealthy_instance_id}"
  docker exec "${unhealthy_container_id}" sh -ceu "rm -f /tmp/dc2-health"

  echo "waiting for Auto Scaling replacement after healthcheck failure"
  replacement_deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  replacement_instance_id=""
  while (( SECONDS < replacement_deadline )); do
    if ! wait_for_inservice_instances "${DESIRED_CAPACITY}"; then
      continue
    fi
    if contains_instance_id "${unhealthy_instance_id}" "${instance_ids[@]}"; then
      sleep "${POLL_INTERVAL_SECONDS}"
      continue
    fi
    current_id=""
    for current_id in "${instance_ids[@]}"; do
      if ! contains_instance_id "${current_id}" "${initial_instance_ids[@]}"; then
        replacement_instance_id="${current_id}"
        break
      fi
    done
    if [[ -n "${replacement_instance_id}" ]]; then
      break
    fi
    sleep "${POLL_INTERVAL_SECONDS}"
  done

  if [[ -z "${replacement_instance_id}" ]]; then
    echo "error: timed out waiting for ASG replacement after healthcheck failure" >&2
    exit 1
  fi

  echo "replacement instance detected: ${replacement_instance_id}"
  curl_instance "${replacement_instance_id}"
fi
