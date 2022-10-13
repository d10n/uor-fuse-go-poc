# uor-fuse-go

Mount a UOR collection to a directory with FUSE.

Usage:

./uor-fuse-go mount <collection> <mountpoint>
./uor-fuse-go mount localhost:5001/test:latest ./mount-dir/


Considerations / TODO:

* Cache data better?
  * Cache will use disk or memory
  * Cache invalidation will be important
* Support fetch with range?
  * Signatures won't be able to be validated unless the full file is cached
    * If the full file is cached, there is no need to fetch with a range
  * Probably requires custom client
* Periodic collection refresh
