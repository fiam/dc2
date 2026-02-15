#!/usr/bin/env bash
set -euo pipefail

# ASG helper script for dc2.
#
# Prerequisite:
# - dc2 is already running (host process or container).
# - aws CLI is installed.
#
# Common examples:
#   ./examples/asg.sh create --desired 3
#   ./examples/asg.sh scale --asg-name asg-multi-123 --desired 5
#   ./examples/asg.sh describe --asg-name asg-multi-123
#   ./examples/asg.sh delete --asg-name asg-multi-123 --delete-launch-template 1
#
# Env vars:
# - DC2_ENDPOINT (default: http://localhost:8080)
# - AWS_REGION / AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN

DC2_ENDPOINT="${DC2_ENDPOINT:-http://localhost:8080}"
AWS_REGION="${AWS_REGION:-us-east-1}"
AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-test}"
AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-test}"
AWS_SESSION_TOKEN="${AWS_SESSION_TOKEN:-}"
AWS_PAGER="${AWS_PAGER:-}"

WAIT_TIMEOUT_SECONDS="${WAIT_TIMEOUT_SECONDS:-60}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-1}"

export AWS_REGION AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_PAGER

usage() {
  cat <<'EOF'
Usage:
  ./examples/asg.sh <command> [options]

Commands:
  create    Create launch template + ASG (defaults to multiple instances)
  scale     Change ASG desired capacity
  delete    Delete ASG (optionally delete launch template)
  describe  Show ASG state and instance IDs/IPs
  help      Show this message

Run command-specific help:
  ./examples/asg.sh create --help
  ./examples/asg.sh scale --help
  ./examples/asg.sh delete --help
  ./examples/asg.sh describe --help
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    die "$1 is required"
  fi
}

is_uint() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

validate_common_wait_options() {
  is_uint "${WAIT_TIMEOUT_SECONDS}" || die "WAIT_TIMEOUT_SECONDS must be an integer"
  is_uint "${POLL_INTERVAL_SECONDS}" || die "POLL_INTERVAL_SECONDS must be an integer"
  (( POLL_INTERVAL_SECONDS > 0 )) || die "POLL_INTERVAL_SECONDS must be > 0"
}

aws_dc2() {
  aws --endpoint-url "${DC2_ENDPOINT}" "$@"
}

asg_count() {
  local asg_name="$1"
  local out
  out="$(
    aws_dc2 autoscaling describe-auto-scaling-groups \
      --auto-scaling-group-names "${asg_name}" \
      --query 'length(AutoScalingGroups)' \
      --output text 2>/dev/null || true
  )"
  if [[ -z "${out}" || "${out}" == "None" ]]; then
    echo 0
    return
  fi
  echo "${out}"
}

inservice_count() {
  local asg_name="$1"
  local out
  out="$(
    aws_dc2 autoscaling describe-auto-scaling-groups \
      --auto-scaling-group-names "${asg_name}" \
      --query "length(AutoScalingGroups[0].Instances[?LifecycleState=='InService'])" \
      --output text 2>/dev/null || true
  )"
  if [[ -z "${out}" || "${out}" == "None" ]]; then
    echo 0
    return
  fi
  echo "${out}"
}

inservice_instance_ids() {
  local asg_name="$1"
  aws_dc2 autoscaling describe-auto-scaling-groups \
    --auto-scaling-group-names "${asg_name}" \
    --query "AutoScalingGroups[0].Instances[?LifecycleState=='InService'].InstanceId" \
    --output text 2>/dev/null || true
}

wait_for_inservice_count() {
  local asg_name="$1"
  local target_count="$2"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  while (( SECONDS < deadline )); do
    local count
    count="$(inservice_count "${asg_name}")"
    if [[ "${count}" == "${target_count}" ]]; then
      return 0
    fi
    sleep "${POLL_INTERVAL_SECONDS}"
  done
  die "timed out waiting for ${target_count} InService instances in ${asg_name}"
}

wait_for_group_deleted() {
  local asg_name="$1"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  while (( SECONDS < deadline )); do
    local count
    count="$(asg_count "${asg_name}")"
    if [[ "${count}" == "0" ]]; then
      return 0
    fi
    sleep "${POLL_INTERVAL_SECONDS}"
  done
  die "timed out waiting for ASG ${asg_name} deletion"
}

cmd_create() {
  local desired=3
  local min_size=""
  local max_size=""
  local instance_image="nginx"
  local instance_type="a1.large"
  local wait_for_inservice=1
  local suffix asg_name lt_name

  while (( $# > 0 )); do
    case "$1" in
      --asg-name)
        asg_name="${2:?missing value for --asg-name}"
        shift 2
        ;;
      --launch-template-name)
        lt_name="${2:?missing value for --launch-template-name}"
        shift 2
        ;;
      --image)
        instance_image="${2:?missing value for --image}"
        shift 2
        ;;
      --instance-type)
        instance_type="${2:?missing value for --instance-type}"
        shift 2
        ;;
      --desired)
        desired="${2:?missing value for --desired}"
        shift 2
        ;;
      --min)
        min_size="${2:?missing value for --min}"
        shift 2
        ;;
      --max)
        max_size="${2:?missing value for --max}"
        shift 2
        ;;
      --wait)
        wait_for_inservice="${2:?missing value for --wait}"
        shift 2
        ;;
      -h|--help)
        cat <<'EOF'
Usage: ./examples/asg.sh create [options]

Options:
  --asg-name <name>                ASG name (default: auto-generated)
  --launch-template-name <name>    Launch template name (default: auto-generated)
  --image <image>                  Instance image (default: nginx)
  --instance-type <type>           Instance type (default: a1.large)
  --desired <n>                    Desired capacity (default: 3)
  --min <n>                        Min size (default: desired)
  --max <n>                        Max size (default: desired)
  --wait <0|1>                     Wait for InService count to reach desired (default: 1)
EOF
        return 0
        ;;
      *)
        die "unknown option for create: $1"
        ;;
    esac
  done

  is_uint "${desired}" || die "--desired must be an integer"
  [[ -n "${min_size}" ]] || min_size="${desired}"
  [[ -n "${max_size}" ]] || max_size="${desired}"
  is_uint "${min_size}" || die "--min must be an integer"
  is_uint "${max_size}" || die "--max must be an integer"
  (( min_size <= desired && desired <= max_size )) || die "expected min <= desired <= max"
  [[ "${wait_for_inservice}" == "0" || "${wait_for_inservice}" == "1" ]] || die "--wait must be 0 or 1"

  suffix="$(date +%s)-${RANDOM}"
  [[ -n "${asg_name:-}" ]] || asg_name="asg-multi-${suffix}"
  [[ -n "${lt_name:-}" ]] || lt_name="lt-multi-${suffix}"

  echo "creating launch template ${lt_name}"
  aws_dc2 ec2 create-launch-template \
    --launch-template-name "${lt_name}" \
    --launch-template-data "{\"ImageId\":\"${instance_image}\",\"InstanceType\":\"${instance_type}\"}" \
    --query 'LaunchTemplate.LaunchTemplateId' \
    --output text >/dev/null

  echo "creating ASG ${asg_name} (min=${min_size} desired=${desired} max=${max_size})"
  aws_dc2 autoscaling create-auto-scaling-group \
    --auto-scaling-group-name "${asg_name}" \
    --min-size "${min_size}" \
    --max-size "${max_size}" \
    --desired-capacity "${desired}" \
    --launch-template "LaunchTemplateName=${lt_name},Version=\$Default"

  if [[ "${wait_for_inservice}" == "1" ]]; then
    echo "waiting for ${desired} InService instance(s)"
    wait_for_inservice_count "${asg_name}" "${desired}"
  fi

  local ids
  ids="$(inservice_instance_ids "${asg_name}")"
  echo
  echo "ASG_NAME=${asg_name}"
  echo "LAUNCH_TEMPLATE_NAME=${lt_name}"
  if [[ -n "${ids}" && "${ids}" != "None" ]]; then
    echo "INSTANCE_IDS=${ids}"
  fi
}

cmd_scale() {
  local asg_name=""
  local desired=""
  local honor_cooldown=0
  local wait_for_inservice=1

  while (( $# > 0 )); do
    case "$1" in
      --asg-name)
        asg_name="${2:?missing value for --asg-name}"
        shift 2
        ;;
      --desired)
        desired="${2:?missing value for --desired}"
        shift 2
        ;;
      --honor-cooldown)
        honor_cooldown=1
        shift
        ;;
      --wait)
        wait_for_inservice="${2:?missing value for --wait}"
        shift 2
        ;;
      -h|--help)
        cat <<'EOF'
Usage: ./examples/asg.sh scale --asg-name <name> --desired <n> [options]

Options:
  --asg-name <name>      Target ASG name
  --desired <n>          New desired capacity
  --honor-cooldown       Pass --honor-cooldown
  --wait <0|1>           Wait for InService count to match desired (default: 1)
EOF
        return 0
        ;;
      *)
        die "unknown option for scale: $1"
        ;;
    esac
  done

  [[ -n "${asg_name}" ]] || die "--asg-name is required"
  is_uint "${desired}" || die "--desired must be an integer"
  [[ "${wait_for_inservice}" == "0" || "${wait_for_inservice}" == "1" ]] || die "--wait must be 0 or 1"

  echo "setting desired capacity for ${asg_name} to ${desired}"
  local args=(
    autoscaling set-desired-capacity
    --auto-scaling-group-name "${asg_name}"
    --desired-capacity "${desired}"
  )
  if [[ "${honor_cooldown}" == "1" ]]; then
    args+=(--honor-cooldown)
  fi
  aws_dc2 "${args[@]}"

  if [[ "${wait_for_inservice}" == "1" ]]; then
    echo "waiting for ${desired} InService instance(s)"
    wait_for_inservice_count "${asg_name}" "${desired}"
  fi

  local ids
  ids="$(inservice_instance_ids "${asg_name}")"
  echo "ASG_NAME=${asg_name}"
  echo "DESIRED_CAPACITY=${desired}"
  if [[ -n "${ids}" && "${ids}" != "None" ]]; then
    echo "INSTANCE_IDS=${ids}"
  fi
}

cmd_delete() {
  local asg_name=""
  local lt_name=""
  local delete_launch_template=0
  local force_delete=1
  local wait_for_delete=1

  while (( $# > 0 )); do
    case "$1" in
      --asg-name)
        asg_name="${2:?missing value for --asg-name}"
        shift 2
        ;;
      --launch-template-name)
        lt_name="${2:?missing value for --launch-template-name}"
        shift 2
        ;;
      --delete-launch-template)
        delete_launch_template="${2:?missing value for --delete-launch-template}"
        shift 2
        ;;
      --force-delete)
        force_delete="${2:?missing value for --force-delete}"
        shift 2
        ;;
      --wait)
        wait_for_delete="${2:?missing value for --wait}"
        shift 2
        ;;
      -h|--help)
        cat <<'EOF'
Usage: ./examples/asg.sh delete --asg-name <name> [options]

Options:
  --asg-name <name>                 Target ASG name
  --launch-template-name <name>     Launch template name (optional)
  --delete-launch-template <0|1>    Delete launch template too (default: 0)
  --force-delete <0|1>              Force delete ASG instances (default: 1)
  --wait <0|1>                      Wait for ASG deletion (default: 1)
EOF
        return 0
        ;;
      *)
        die "unknown option for delete: $1"
        ;;
    esac
  done

  [[ -n "${asg_name}" ]] || die "--asg-name is required"
  [[ "${delete_launch_template}" == "0" || "${delete_launch_template}" == "1" ]] || die "--delete-launch-template must be 0 or 1"
  [[ "${force_delete}" == "0" || "${force_delete}" == "1" ]] || die "--force-delete must be 0 or 1"
  [[ "${wait_for_delete}" == "0" || "${wait_for_delete}" == "1" ]] || die "--wait must be 0 or 1"

  if [[ "${delete_launch_template}" == "1" && -z "${lt_name}" ]]; then
    lt_name="$(
      aws_dc2 autoscaling describe-auto-scaling-groups \
        --auto-scaling-group-names "${asg_name}" \
        --query 'AutoScalingGroups[0].LaunchTemplate.LaunchTemplateName' \
        --output text 2>/dev/null || true
    )"
    if [[ "${lt_name}" == "None" ]]; then
      lt_name=""
    fi
  fi

  local args=(
    autoscaling delete-auto-scaling-group
    --auto-scaling-group-name "${asg_name}"
  )
  if [[ "${force_delete}" == "1" ]]; then
    args+=(--force-delete)
  fi

  echo "deleting ASG ${asg_name}"
  aws_dc2 "${args[@]}"

  if [[ "${wait_for_delete}" == "1" ]]; then
    echo "waiting for ASG deletion"
    wait_for_group_deleted "${asg_name}"
  fi

  if [[ "${delete_launch_template}" == "1" ]]; then
    if [[ -z "${lt_name}" ]]; then
      echo "warning: launch template name is unknown; skipping launch template delete" >&2
    else
      echo "deleting launch template ${lt_name}"
      aws_dc2 ec2 delete-launch-template --launch-template-name "${lt_name}" >/dev/null
      echo "LAUNCH_TEMPLATE_NAME=${lt_name} deleted"
    fi
  fi
}

cmd_describe() {
  local asg_name=""

  while (( $# > 0 )); do
    case "$1" in
      --asg-name)
        asg_name="${2:?missing value for --asg-name}"
        shift 2
        ;;
      -h|--help)
        cat <<'EOF'
Usage: ./examples/asg.sh describe --asg-name <name>
EOF
        return 0
        ;;
      *)
        die "unknown option for describe: $1"
        ;;
    esac
  done

  [[ -n "${asg_name}" ]] || die "--asg-name is required"

  aws_dc2 autoscaling describe-auto-scaling-groups \
    --auto-scaling-group-names "${asg_name}" \
    --query 'AutoScalingGroups[0].{AutoScalingGroupName:AutoScalingGroupName,MinSize:MinSize,DesiredCapacity:DesiredCapacity,MaxSize:MaxSize,LaunchTemplate:LaunchTemplate,Instances:Instances[].{InstanceId:InstanceId,LifecycleState:LifecycleState,HealthStatus:HealthStatus}}' \
    --output json

  local ids_raw
  ids_raw="$(inservice_instance_ids "${asg_name}")"
  if [[ -n "${ids_raw}" && "${ids_raw}" != "None" ]]; then
    read -r -a ids <<<"${ids_raw}"
    echo
    aws_dc2 ec2 describe-instances \
      --instance-ids "${ids[@]}" \
      --query 'Reservations[].Instances[].{InstanceId:InstanceId,State:State.Name,PrivateIp:PrivateIpAddress,PublicIp:PublicIpAddress}' \
      --output table
  fi
}

main() {
  require_cmd aws
  validate_common_wait_options

  if (( $# == 0 )); then
    usage
    exit 1
  fi

  local cmd="$1"
  shift
  case "${cmd}" in
    create) cmd_create "$@" ;;
    scale) cmd_scale "$@" ;;
    delete) cmd_delete "$@" ;;
    describe) cmd_describe "$@" ;;
    help|-h|--help) usage ;;
    *) die "unknown command: ${cmd}" ;;
  esac
}

main "$@"

