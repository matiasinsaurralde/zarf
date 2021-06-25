# Earthfile

ARG CONFIG="config.yaml"

# Create the env file template
envfile:
  LOCALLY

  RUN printf '# Generated by earthly +envfile \nIB_USER=replace_me\nIB_PASS=replace_me' > .env
  
# Test the deployment with vagrant, default to ubuntu.  Usage: `OS=rhel7 earthly +test`
test:
  LOCALLY
  RUN _os="${OS:=ubuntu}" vagrant destroy -f && vagrant up --no-color $OS && \
      echo -e "\n\n\n\033[1;93m  ✅ BUILD COMPLETE.  To access this environment, run \"vagrant ssh $OS\"\n\n\n"

test-destroy:
  LOCALLY
  RUN vagrant destroy -f

# Copy the helm 3 binary
helm:
  FROM alpine/helm:3.5.3
  SAVE ARTIFACT /usr/bin/helm

# Copy the yq 4 binary
yq:
  FROM mikefarah/yq
  SAVE ARTIFACT /usr/bin/yq

# Bring the zarf build artifact in
zarf:
  FROM registry1.dso.mil/ironbank/redhat/ubi/ubi8
  COPY ./cli+build/zarf zarf
  SAVE ARTIFACT zarf

# The baseline image with common binaries and $CONFIG
common:
  FROM registry1.dso.mil/ironbank/redhat/ubi/ubi8
  WORKDIR /payload

  COPY +helm/helm /usr/bin
  COPY +yq/yq /usr/bin
  COPY $CONFIG .

# Fetch the helm charts specified in $CONFIG 
charts:
  FROM +common

  RUN mkdir charts

  RUN yq e '.charts[] | .name + " " + .url' $CONFIG | \
      while read line ; do echo "repo add $line" | xargs -t helm; done

  RUN yq e '.charts[] | .name + "/" + .name + " -d ./charts --version " + .version' $CONFIG | \
      while read line ; do echo "pull $line" | xargs -t helm; done

  SAVE ARTIFACT charts

# Fetch the k3s version specified in $CONFIG
k3s:
  FROM +common

  # Add a version flag to the init-k3s script to ensure we cache-bust when pulling down a version for the installer (this is ignored by the server)
  RUN K3S_VERSION=$(yq e '.k3s.version' $CONFIG) && \
      curl -fL "https://github.com/k3s-io/k3s/releases/download/$K3S_VERSION/{k3s,k3s-images.txt,sha256sum-amd64.txt}" -o "#1" && \
      sha256sum -c --ignore-missing "sha256sum-amd64.txt"

  SAVE ARTIFACT *

# Fetch the k9s version specified in $CONFIG
k9s:
  FROM +common

  RUN K9S_VERSION=$(yq e '.k9s.version' $CONFIG) && \
      curl -fL "https://github.com/derailed/k9s/releases/download/$K9S_VERSION/k9s_${K9S_VERSION}_Linux_x86_64.tar.gz" | tar xvz
      # todo: add checksums.txt without making everything sad

  SAVE ARTIFACT *

# Fetch k3s images and images specified in $CONFIG
images:
  FROM +common

  COPY +k3s/k3s-images.txt k3s-images.txt

  COPY +zarf/zarf .

  RUN --secret IB_USER=+secrets/IB_USER --secret IB_PASS=+secrets/IB_PASS \
      k3s_images=$(cat "k3s-images.txt" | tr "\n" " ") && \
      app_images=$(yq e '.images | join(" ")' $CONFIG) && \
      export images="$app_images $k3s_images" && \
      echo "Cloning: $images" | tr " " "\n " && \
      ./zarf registry login registry1.dso.mil -u $IB_USER -p $IB_PASS && \
      ./zarf registry pull $images images.tar

  SAVE ARTIFACT images.tar

# Compress all assets in a single tar.zst file
compress: 
  FROM +common

  # Pull in artifacts from other build stages
  COPY +k3s/* bin/
  COPY +k9s/k9s bin/
  COPY +charts/charts charts
  COPY +images/images.tar images/images.tar

  # Quick housekeeping
  RUN rm -f bin/*.txt *.yaml && mkdir -p rpms

  # Pull in local resources
  COPY init-manifests manifests

  COPY +zarf/zarf .

  # Compress the tarball
  RUN ./zarf archiver compress . /export.tar.zst

  SAVE ARTIFACT /export.tar.zst
  
# Final packaging of the binary/tarball/checksum assets
build:
  FROM +common

  COPY +zarf/zarf .

  # Copy the final compressed tarball for shasum / export
  COPY +compress/export.tar.zst zarf-initialize.tar.zst

  RUN sha256sum -b zarf* > zarf.sha256

  RUN ls -lah zarf*

  SAVE ARTIFACT zarf* AS LOCAL ./build/

# Test basic image pull for CI
test-ci:
  FROM registry1.dso.mil/ironbank/redhat/ubi/ubi8