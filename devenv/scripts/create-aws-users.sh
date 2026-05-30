#!/usr/bin/env bash
set -euxo pipefail

S3_ENDPOINT="http://localhost:8333"
IAM_ENDPOINT="http://localhost:8333"
AWS_PROFILE="${AWS_PROFILE:-seaweedfs-testenv}"
USERS=("foo" "bar" "baz")

for USER in "${USERS[@]}"; do
  BUCKET="${USER}-bucket"

  # Create S3 bucket
  aws s3 mb "s3://${BUCKET}" \
    --profile "$AWS_PROFILE" \
    --endpoint-url "$S3_ENDPOINT"

  # Create IAM user
  aws iam create-user \
    --user-name "$USER" \
    --endpoint-url "$IAM_ENDPOINT" \
    --profile "$AWS_PROFILE"

  # Create IAM policy granting full access to the user's bucket
  POLICY_DOC=$(cat <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:*"],
      "Resource": [
        "arn:aws:s3:::${BUCKET}",
        "arn:aws:s3:::${BUCKET}/*"
      ]
    }
  ]
}
EOF
)

  aws iam create-policy \
    --policy-name "${USER}-bucket-policy" \
    --policy-document "$POLICY_DOC" \
    --endpoint-url "$IAM_ENDPOINT" \
    --profile "$AWS_PROFILE"

  # Attach the policy to the user
  aws iam attach-user-policy \
    --user-name "$USER" \
    --policy-arn "arn:aws:iam:::policy/${USER}-bucket-policy" \
    --endpoint-url "$IAM_ENDPOINT" \
    --profile "$AWS_PROFILE"

  # Create access key for the user and print it
  echo "Access key for user '${USER}':"
  aws iam create-access-key \
    --user-name "$USER" \
    --endpoint-url "$IAM_ENDPOINT" \
    --profile "$AWS_PROFILE"
done
