package docker

import (
	"context"

	"emperror.dev/errors"
	dockerImage "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

func ImageExistsLocally(ctx context.Context, cli *client.Client, ref string) (bool, error) {
	images, err := cli.ImageList(ctx, dockerImage.ListOptions{})
	if err != nil {
		return false, errors.Wrap(err, "environment/docker: failed to list images")
	}

	for _, img := range images {
		if imageReferenceExists(ref, img.RepoTags) || imageReferenceExists(ref, img.RepoDigests) {
			return true, nil
		}
	}

	return false, nil
}

func imageReferenceExists(ref string, refs []string) bool {
	for _, existing := range refs {
		if existing == ref {
			return true
		}
	}

	return false
}
