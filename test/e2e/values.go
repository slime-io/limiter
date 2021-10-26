package e2e

var (
	testResourceToDelete []*TestResource
	nsSlime              = "temppp"
	nsApps               = "temppp"
	test                 = "test/e2e/testdata/install"
	slimebootName        = "slime-boot"
	istiodLabelKey       = "istio.io/rev"
	istiodLabelV         = "1-10-2"
	slimebootTag          = "v0.2.3-5bf313f"
	limitTag           = "v0.4"
)

type TestResource struct {
	Namespace string
	Contents  string
	Selectors []string
}
