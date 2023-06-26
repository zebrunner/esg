# 
# get base directory
BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$BASEDIR/../router.env"

listProfiles="aws iam list-instance-profiles"
lisRolesPolicies="aws iam list-attached-role-policies" # --role-name
getPolicy="aws iam get-policy" # --policy-arn
describePolicy="aws iam get-policy-version" # --policy-arn --version-id
# add region to command if present
if [ ! -z "$AWS_REGION" ]; then
  listProfiles="$listProfiles --region $AWS_REGION"
  lisRolesPolicies="$lisRolesPolicies --region $AWS_REGION"
  getPolicy="$getPolicy --region $AWS_REGION"
  describePolicy="$describePolicy --region $AWS_REGION"
fi

# get all instance profiles
PROFILES=`$listProfiles | jq -r '.[][]  |[[{key:.Arn, value:{"RoleNames":[.Roles[] | .RoleName] }}] | from_entries ]' | jq -s 'flatten(1)'` 

# iterate by profile
echo "$PROFILES" | jq -c '.[]' | while read -r profile; do
  # write profiles roles into array
  readarray -t roleNames < <(echo ${profile} | jq -r '.[].RoleNames[]')

  # iterate by role
  for role in "${roleNames[@]}"; do
    # write role attached policies Arns to array  
    readarray -t policiesArns < <($lisRolesPolicies --role-name $role | jq -r '.[][] |[ if .PolicyArn != null then .PolicyArn else empty end]' | jq -s 'flatten(1)' | jq -r '.[]')
    # iterate by policyArn and save results to the policies array 
    policies=
    for policyArn in "${policiesArns[@]}"; do
      # get policy's Description, DefaultVersionId
      policyGeneralInfo=`$getPolicy --policy-arn $policyArn | jq '.[] | if .Description != null then [{key:.Arn, value: {PolicyName, Description, DefaultVersionId}}] else [{key:.Arn, value: {PolicyName, DefaultVersionId}}] end | from_entries | .[]'`
      
      # parse DefaultVersionId to var
      policyVersion=`echo $policyGeneralInfo | jq -r '.DefaultVersionId'`
      # get policy's Actions and Resources
      policyDescr=`$describePolicy --policy-arn $policyArn --version-id $policyVersion | jq '.[] | .Document.Statement[] | [{key:"PolicyDocument", value:{Action, Resource}}] | from_entries | .[]'`
      # unite collected data
      finalRolePolicy=`jq -s 'add' <(echo "$policyGeneralInfo") <(echo "$policyDescr")`
      # save result to the policies array
      policies+="$finalRolePolicy"
    done
    # add policies to role
    policies=`echo $policies | jq '[.]' | jq -s 'flatten(1)' | jq '[{key:"Policies", value: .}] | from_entries'`
    roleJson='{"RoleName": "'$role'"}'
    jq -s 'add' <(echo "$roleJson") <(echo "$policies")
  done

done