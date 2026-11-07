import Cluster from '@shell/models/management.cattle.io.cluster';

// Steve's stand-in for the local cluster has status.version, not the
// status.info that Rancher's own controllers populate.
export default class LocalCluster extends Cluster {
  get statusInfo() {
    const version = this.status?.version;

    return {
      kubernetesVersion: version?.gitVersion,
      arch:              version?.platform?.split('/')[1],
    };
  }
}
