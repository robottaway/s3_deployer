README
-------

s3\_deployer is a tool for  downloading zip packages containing and then installing and booting them.

Target is the Real Time groups applications that use the [ostrich-cluster](https://github.com/PagerDuty/chef/blob/master/site-cookbooks/pd-ostrich-cluster/templates/default/rt_init.erb) recipe for starting/stopping.

INSTALL
--------

To get a copy of the project and build and install the application do the following:

```
git clone --recursive git@github.com:robottaway/s3_deployer.git
cd s3_deployer
export GOPATH=`pwd`
go install github.com/PagerDuty/s3_deployer
bin/s3_deployer install <
```

