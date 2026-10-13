#!/usr/bin/env bash
set -euo pipefail
: "${RELEASE_VERSION:?}" "${RELEASE_SHA:?}" "${GITHUB_REPOSITORY:?}" "${RUNNER_TEMP:?}"
# No implicit upstream/fork registry. Local builds are the default until the
# operator supplies an explicit destination and enables publication.
if [[ ${DRY_RUN:-true} != true ]]; then
  [[ ${IMAGE_PUBLISH_ENABLED:-false} == true ]] || { echo 'Image publication is disabled' >&2; exit 1; }
  : "${IMAGE_REPOSITORY:?Set the destination image repository before publishing}"
fi
repository=${IMAGE_REPOSITORY:-local/sub2api-mimo}
case "$(printf %s "$repository" | tr '[:upper:]' '[:lower:]')" in
  weishaw/*|docker.io/weishaw/*|index.docker.io/weishaw/*|ghcr.io/wei-shaw/*|ghcr.io/weishaw/*|bayma888/*|docker.io/bayma888/*|ghcr.io/bayma888/*)
    echo 'Refusing to publish or tag an upstream image repository' >&2; exit 1 ;;
esac
# Repository must not include a tag, digest or URL scheme (registry ports allowed).
[[ "$repository" =~ ^[a-z0-9][a-z0-9.:-]*/[a-z0-9._/-]+$ && "$repository" != *://* && "$repository" != *@* ]] || { echo 'Invalid IMAGE_REPOSITORY (use registry/namespace/image without a tag)' >&2; exit 1; }
registries=("$repository")
arches=(amd64 arm64)
if [[ ${SIMPLE_RELEASE:-false} == true ]]; then arches=(amd64); fi
for arch in "${arches[@]}"; do
  args=(--platform "linux/$arch" --file ".release-context/$arch/Dockerfile"
    --label "org.opencontainers.image.version=$RELEASE_VERSION"
    --label "org.opencontainers.image.revision=$RELEASE_SHA"
    --label "org.opencontainers.image.source=https://github.com/$GITHUB_REPOSITORY")
  for registry in "${registries[@]}"; do
    args+=(--tag "$registry:$RELEASE_VERSION-$arch")
    if [[ ${SIMPLE_RELEASE:-false} == true ]]; then
      args+=(--tag "$registry:$RELEASE_VERSION" --tag "$registry:latest")
    fi
  done
  if [[ ${DRY_RUN:-true} == true ]]; then
    args+=(--output "type=oci,dest=$RUNNER_TEMP/sub2api-$arch.oci.tar")
  else
    args+=(--push)
  fi
  docker buildx build "${args[@]}" ".release-context/$arch"
done
if [[ ${DRY_RUN:-true} != true && ${SIMPLE_RELEASE:-false} != true ]]; then
  major=${RELEASE_VERSION%%.*}
  minor=${RELEASE_VERSION#*.}; minor=${minor%%.*}
  for registry in "${registries[@]}"; do
    docker buildx imagetools create \
      --tag "$registry:$RELEASE_VERSION" --tag "$registry:latest" \
      --tag "$registry:$major.$minor" --tag "$registry:$major" \
      "$registry:$RELEASE_VERSION-amd64" "$registry:$RELEASE_VERSION-arm64"
  done
fi
