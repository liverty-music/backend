# Every backend image, built in one Bake run by the push path of
# .github/workflows/deploy.yml. One run builds the shared `builder` and
# `build-static` stages of the Dockerfile once for all targets.
#
# Keep `images` in step with the release-path matrix in deploy.yml: the
# release promotes `<image>:<sha>` for exactly these names.
#
#   docker buildx bake --print                       # resolved definition
#   IMAGE_REPO=local SHA=dev docker buildx bake      # build without pushing

variable "IMAGE_REPO" {
  # <region>-docker.pkg.dev/<project>/backend
  default = "local"
}

variable "SHA" {
  default = "dev"
}

variable "images" {
  default = [
    { name = "api", target = "server" },
    { name = "consumer", target = "consumer" },
    { name = "concert-discovery", target = "concert-discovery" },
    { name = "artist-image-sync", target = "artist-image-sync" },
    { name = "official-site-refresh", target = "official-site-refresh" },
    { name = "sales-phase-discovery", target = "sales-phase-discovery" },
    { name = "sales-reminders", target = "sales-reminders" },
    { name = "series-image-consumer", target = "series-image-consumer" },
  ]
}

group "default" {
  targets = [for img in images : img.name]
}

target "image" {
  name    = img.name
  matrix  = { img = images }
  context = "."
  target  = img.target
  tags = [
    "${IMAGE_REPO}/${img.name}:latest",
    "${IMAGE_REPO}/${img.name}:main",
    "${IMAGE_REPO}/${img.name}:${SHA}",
  ]
  # One GitHub Actions cache scope per image, the scopes the per-image jobs
  # used before. A shared scope would let the images overwrite each other's
  # cache manifest.
  cache-from = ["type=gha,scope=${img.name}"]
  cache-to   = ["type=gha,mode=max,scope=${img.name}"]
}
