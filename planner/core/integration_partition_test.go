// Copyright 2021 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package core_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"

	. "github.com/pingcap/check"
	"github.com/pingcap/parser/auth"
	"github.com/pingcap/tidb/domain"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/session"
	"github.com/pingcap/tidb/util/israce"
	"github.com/pingcap/tidb/util/testkit"
	"github.com/pingcap/tidb/util/testutil"
)

var _ = SerialSuites(&testIntegrationPartitionSerialSuite{})

type testIntegrationPartitionSerialSuite struct {
	testData testutil.TestData
	store    kv.Storage
	dom      *domain.Domain
}

func (s *testIntegrationPartitionSerialSuite) SetUpSuite(c *C) {
	var err error
	s.testData, err = testutil.LoadTestSuiteData("testdata", "integration_partition_suite")
	c.Assert(err, IsNil)
}

func (s *testIntegrationPartitionSerialSuite) TearDownSuite(c *C) {
	c.Assert(s.testData.GenerateOutputIfNeeded(), IsNil)
}

func (s *testIntegrationPartitionSerialSuite) SetUpTest(c *C) {
	var err error
	s.store, s.dom, err = newStoreWithBootstrap()
	c.Assert(err, IsNil)
}

func (s *testIntegrationPartitionSerialSuite) TearDownTest(c *C) {
	s.dom.Close()
	err := s.store.Close()
	c.Assert(err, IsNil)
}
func (s *testIntegrationPartitionSerialSuite) TestListPartitionPushDown(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_push_down")
	tk.MustExec("use list_push_down")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec(`create table tlist (a int) partition by list (a) (
    partition p0 values in (0, 1, 2),
    partition p1 values in (3, 4, 5))`)
	tk.MustExec("set @@tidb_partition_prune_mode = 'static'")

	var input []string
	var output []struct {
		SQL  string
		Plan []string
	}
	s.testData.GetTestCases(c, &input, &output)
	for i, tt := range input {
		s.testData.OnRecord(func() {
			output[i].SQL = tt
			output[i].Plan = s.testData.ConvertRowsToStrings(tk.MustQuery(tt).Rows())
		})
		tk.MustQuery(tt).Check(testkit.Rows(output[i].Plan...))
	}
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionPruning(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_pruning")
	tk.MustExec("use list_partition_pruning")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec(`create table tlist (a int) partition by list (a) (
    partition p0 values in (0, 1, 2),
    partition p1 values in (3, 4, 5),
    partition p2 values in (6, 7, 8),
    partition p3 values in (9, 10, 11))`)

	var input []string
	var output []struct {
		SQL         string
		DynamicPlan []string
		StaticPlan  []string
	}
	s.testData.GetTestCases(c, &input, &output)
	for i, tt := range input {
		s.testData.OnRecord(func() {
			output[i].SQL = tt
			tk.MustExec("set @@tidb_partition_prune_mode = 'dynamic'")
			output[i].DynamicPlan = s.testData.ConvertRowsToStrings(tk.MustQuery(tt).Rows())
			tk.MustExec("set @@tidb_partition_prune_mode = 'static'")
			output[i].StaticPlan = s.testData.ConvertRowsToStrings(tk.MustQuery(tt).Rows())
		})
		tk.MustExec("set @@tidb_partition_prune_mode = 'dynamic'")
		tk.MustQuery(tt).Check(testkit.Rows(output[i].DynamicPlan...))
		tk.MustExec("set @@tidb_partition_prune_mode = 'static'")
		tk.MustQuery(tt).Check(testkit.Rows(output[i].StaticPlan...))
	}
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionFunctions(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_pruning")
	tk.MustExec("use list_partition_pruning")
	tk.MustExec("set tidb_enable_list_partition = 1")
	tk.MustExec("set @@tidb_partition_prune_mode = 'static'")

	var input []string
	var output []struct {
		SQL     string
		Results []string
	}
	s.testData.GetTestCases(c, &input, &output)
	for i, tt := range input {
		s.testData.OnRecord(func() {
			output[i].SQL = tt
			output[i].Results = nil
			if strings.Contains(tt, "select") {
				output[i].Results = s.testData.ConvertRowsToStrings(tk.MustQuery(tt).Sort().Rows())
			}
		})

		if strings.Contains(tt, "select") {
			tk.MustQuery(tt).Sort().Check(testkit.Rows(output[i].Results...))
		} else {
			tk.MustExec(tt)
		}
	}
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionOrderLimit(c *C) {
	if israce.RaceEnabled {
		c.Skip("skip race test")
	}

	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_order_limit")
	tk.MustExec("use list_partition_order_limit")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec(`create table tlist (a int, b int) partition by list(a) (` +
		` partition p0 values in ` + genListPartition(0, 20) +
		`, partition p1 values in ` + genListPartition(20, 40) +
		`, partition p2 values in ` + genListPartition(40, 60) +
		`, partition p3 values in ` + genListPartition(60, 80) +
		`, partition p4 values in ` + genListPartition(80, 100) + `)`)
	tk.MustExec(`create table tnormal (a int, b int)`)

	vals := ""
	for i := 0; i < 50; i++ {
		if vals != "" {
			vals += ", "
		}
		vals += fmt.Sprintf("(%v, %v)", i*2+rand.Intn(2), i*2+rand.Intn(2))
	}
	tk.MustExec(`insert into tlist values ` + vals)
	tk.MustExec(`insert into tnormal values ` + vals)

	for _, orderCol := range []string{"a", "b"} {
		for _, limitNum := range []string{"1", "5", "20", "100"} {
			randCond := fmt.Sprintf("where %v > %v", []string{"a", "b"}[rand.Intn(2)], rand.Intn(100))
			rs := tk.MustQuery(fmt.Sprintf(`select * from tnormal %v order by %v limit %v`, randCond, orderCol, limitNum)).Sort()

			tk.MustExec("set @@tidb_partition_prune_mode = 'dynamic'")
			rsDynamic := tk.MustQuery(fmt.Sprintf(`select * from tlist %v order by %v limit %v`, randCond, orderCol, limitNum)).Sort()

			tk.MustExec("set @@tidb_partition_prune_mode = 'static'")
			rsStatic := tk.MustQuery(fmt.Sprintf(`select * from tlist %v order by %v limit %v`, randCond, orderCol, limitNum)).Sort()

			rs.Check(rsDynamic.Rows())
			rs.Check(rsStatic.Rows())
		}
	}
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionAgg(c *C) {
	if israce.RaceEnabled {
		c.Skip("skip race test")
	}

	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_agg")
	tk.MustExec("use list_partition_agg")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec(`create table tlist (a int, b int) partition by list(a) (` +
		` partition p0 values in ` + genListPartition(0, 20) +
		`, partition p1 values in ` + genListPartition(20, 40) +
		`, partition p2 values in ` + genListPartition(40, 60) +
		`, partition p3 values in ` + genListPartition(60, 80) +
		`, partition p4 values in ` + genListPartition(80, 100) + `)`)
	tk.MustExec(`create table tnormal (a int, b int)`)

	vals := ""
	for i := 0; i < 50; i++ {
		if vals != "" {
			vals += ", "
		}
		vals += fmt.Sprintf("(%v, %v)", rand.Intn(100), rand.Intn(100))
	}
	tk.MustExec(`insert into tlist values ` + vals)
	tk.MustExec(`insert into tnormal values ` + vals)

	for _, aggFunc := range []string{"min", "max", "sum", "count"} {
		c1, c2 := "a", "b"
		for i := 0; i < 2; i++ {
			rs := tk.MustQuery(fmt.Sprintf(`select %v, %v(%v) from tnormal group by %v`, c1, aggFunc, c2, c1)).Sort()

			tk.MustExec("set @@tidb_partition_prune_mode = 'dynamic'")
			rsDynamic := tk.MustQuery(fmt.Sprintf(`select %v, %v(%v) from tlist group by %v`, c1, aggFunc, c2, c1)).Sort()

			tk.MustExec("set @@tidb_partition_prune_mode = 'static'")
			rsStatic := tk.MustQuery(fmt.Sprintf(`select %v, %v(%v) from tlist group by %v`, c1, aggFunc, c2, c1)).Sort()

			rs.Check(rsDynamic.Rows())
			rs.Check(rsStatic.Rows())
		}
	}
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionDML(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_dml")
	tk.MustExec("use list_partition_dml")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec(`create table tlist (a int) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14))`)

	tk.MustExec("insert into tlist partition(p0) values (0), (1)")
	tk.MustExec("insert into tlist partition(p0, p1) values (2), (3), (8), (9)")
	c.Assert(tk.ExecToErr("insert into tlist partition(p0) values (9)"), ErrorMatches, ".*Found a row not matching the given partition set.*")
	c.Assert(tk.ExecToErr("insert into tlist partition(p3) values (20)"), ErrorMatches, ".*Unknown partition.*")

	tk.MustExec("update tlist partition(p0) set a=a+1")
	tk.MustQuery("select a from tlist order by a").Check(testkit.Rows("1", "2", "3", "4", "8", "9"))
	tk.MustExec("update tlist partition(p0, p1) set a=a-1")
	tk.MustQuery("select a from tlist order by a").Check(testkit.Rows("0", "1", "2", "3", "7", "8"))

	tk.MustExec("delete from tlist partition(p1)")
	tk.MustQuery("select a from tlist order by a").Check(testkit.Rows("0", "1", "2", "3"))
	tk.MustExec("delete from tlist partition(p0, p2)")
	tk.MustQuery("select a from tlist order by a").Check(testkit.Rows())
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionCreation(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_cre")
	tk.MustExec("use list_partition_cre")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)

	// with UK
	tk.MustExec("create table tuk1 (a int, b int, unique key(a)) partition by list (a) (partition p0 values in (0))")
	c.Assert(tk.ExecToErr("create table tuk2 (a int, b int, unique key(a)) partition by list (b) (partition p0 values in (0))"), ErrorMatches, ".*UNIQUE INDEX must include all columns.*")
	c.Assert(tk.ExecToErr("create table tuk2 (a int, b int, unique key(a), unique key(b)) partition by list (a) (partition p0 values in (0))"), ErrorMatches, ".*UNIQUE INDEX must include all columns.*")

	// with PK
	tk.MustExec("create table tpk1 (a int, b int, primary key(a)) partition by list (a) (partition p0 values in (0))")
	tk.MustExec("create table tpk2 (a int, b int, primary key(a, b)) partition by list (a) (partition p0 values in (0))")

	// with IDX
	tk.MustExec("create table tidx1 (a int, b int, key(a), key(b)) partition by list (a) (partition p0 values in (0))")
	tk.MustExec("create table tidx2 (a int, b int, key(a, b), key(b)) partition by list (a) (partition p0 values in (0))")

	// with expression
	tk.MustExec("create table texp1 (a int, b int) partition by list(a-10000) (partition p0 values in (0))")
	tk.MustExec("create table texp2 (a int, b int) partition by list(a%b) (partition p0 values in (0))")
	tk.MustExec("create table texp3 (a int, b int) partition by list(a*b) (partition p0 values in (0))")
	c.Assert(tk.ExecToErr("create table texp4 (a int, b int) partition by list(a|b) (partition p0 values in (0))"), ErrorMatches, ".*This partition function is not allowed.*")
	c.Assert(tk.ExecToErr("create table texp4 (a int, b int) partition by list(a^b) (partition p0 values in (0))"), ErrorMatches, ".*This partition function is not allowed.*")
	c.Assert(tk.ExecToErr("create table texp4 (a int, b int) partition by list(a&b) (partition p0 values in (0))"), ErrorMatches, ".*This partition function is not allowed.*")
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionDDL(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_ddl")
	tk.MustExec("use list_partition_ddl")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)

	// index
	tk.MustExec(`create table tlist (a int, b int) partition by list (a) (partition p0 values in (0))`)
	c.Assert(tk.ExecToErr(`alter table tlist add primary key (b)`), ErrorMatches, ".*must include all.*") // add pk
	tk.MustExec(`alter table tlist add primary key (a)`)
	c.Assert(tk.ExecToErr(`alter table tlist add unique key (b)`), ErrorMatches, ".*must include all.*") // add uk
	tk.MustExec(`alter table tlist add key (b)`)                                                         // add index
	tk.MustExec(`alter table tlist rename index b to bb`)
	tk.MustExec(`alter table tlist drop index bb`)

	// column
	tk.MustExec(`alter table tlist add column c varchar(8)`)
	tk.MustExec(`alter table tlist rename column c to cc`)
	tk.MustExec(`alter table tlist drop column cc`)

	// table
	tk.MustExec(`alter table tlist rename to tlistxx`)
	tk.MustExec(`truncate tlistxx`)
	tk.MustExec(`drop table tlistxx`)
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionOperations(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_op")
	tk.MustExec("use list_partition_op")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)

	tk.MustExec(`create table tlist (a int) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14),
    partition p3 values in (15, 16, 17, 18, 19))`)

	// truncate
	tk.MustExec("insert into tlist values (0), (5), (10), (15)")
	tk.MustQuery("select * from tlist").Sort().Check(testkit.Rows("0", "10", "15", "5"))
	tk.MustExec("alter table tlist truncate partition p0")
	tk.MustQuery("select * from tlist").Sort().Check(testkit.Rows("10", "15", "5"))
	tk.MustExec("alter table tlist truncate partition p1, p2")
	tk.MustQuery("select * from tlist").Sort().Check(testkit.Rows("15"))

	// drop partition
	tk.MustExec("insert into tlist values (0), (5), (10)")
	tk.MustQuery("select * from tlist").Sort().Check(testkit.Rows("0", "10", "15", "5"))
	tk.MustExec("alter table tlist drop partition p0")
	tk.MustQuery("select * from tlist").Sort().Check(testkit.Rows("10", "15", "5"))
	c.Assert(tk.ExecToErr("select * from tlist partition (p0)"), ErrorMatches, ".*Unknown partition.*")
	tk.MustExec("alter table tlist drop partition p1, p2")
	tk.MustQuery("select * from tlist").Sort().Check(testkit.Rows("15"))
	c.Assert(tk.ExecToErr("select * from tlist partition (p1)"), ErrorMatches, ".*Unknown partition.*")
	c.Assert(tk.ExecToErr("alter table tlist drop partition p3"), ErrorMatches, ".*Cannot remove all partitions.*")

	// add partition
	tk.MustExec("alter table tlist add partition (partition p0 values in (0, 1, 2, 3, 4))")
	tk.MustExec("alter table tlist add partition (partition p1 values in (5, 6, 7, 8, 9), partition p2 values in (10, 11, 12, 13, 14))")
	tk.MustExec("insert into tlist values (0), (5), (10)")
	tk.MustQuery("select * from tlist").Sort().Check(testkit.Rows("0", "10", "15", "5"))
	c.Assert(tk.ExecToErr("alter table tlist add partition (partition pxxx values in (4))"), ErrorMatches, ".*Multiple definition.*")
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionPrivilege(c *C) {
	se, err := session.CreateSession4Test(s.store)
	c.Assert(err, IsNil)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "root", Hostname: "%"}, nil, nil), IsTrue)
	tk := testkit.NewTestKit(c, s.store)
	tk.Se = se
	tk.MustExec("create database list_partition_pri")
	tk.MustExec("use list_partition_pri")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec(`create table tlist (a int) partition by list (a) (partition p0 values in (0), partition p1 values in (1))`)

	tk.MustExec(`create user 'priv_test'@'%'`)
	tk.MustExec(`grant select on list_partition_pri.tlist to 'priv_test'`)

	tk1 := testkit.NewTestKit(c, s.store)
	se, err = session.CreateSession4Test(s.store)
	c.Assert(err, IsNil)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "priv_test", Hostname: "%"}, nil, nil), IsTrue)
	tk1.Se = se
	tk1.MustExec(`use list_partition_pri`)
	c.Assert(tk1.ExecToErr(`alter table tlist truncate partition p0`), ErrorMatches, ".*denied.*")
	c.Assert(tk1.ExecToErr(`alter table tlist drop partition p0`), ErrorMatches, ".*denied.*")
	c.Assert(tk1.ExecToErr(`alter table tlist add partition (partition p2 values in (2))`), ErrorMatches, ".*denied.*")
	c.Assert(tk1.ExecToErr(`insert into tlist values (1)`), ErrorMatches, ".*denied.*")
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionShardBits(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_shard_bits")
	tk.MustExec("use list_partition_shard_bits")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)

	tk.MustExec(`create table tlist (a int) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14))`)
	tk.MustExec("insert into tlist values (0), (1), (5), (6), (10), (12)")

	tk.MustQuery("select * from tlist").Sort().Check(testkit.Rows("0", "1", "10", "12", "5", "6"))
	tk.MustQuery("select * from tlist partition (p0)").Sort().Check(testkit.Rows("0", "1"))
	tk.MustQuery("select * from tlist partition (p1, p2)").Sort().Check(testkit.Rows("10", "12", "5", "6"))
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionSplitRegion(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_split_region")
	tk.MustExec("use list_partition_split_region")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)

	tk.MustExec(`create table tlist (a int, key(a)) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14))`)
	tk.MustExec("insert into tlist values (0), (1), (5), (6), (10), (12)")

	tk.MustExec(`split table tlist index a between (2) and (15) regions 10`)
	tk.MustQuery("select * from tlist").Sort().Check(testkit.Rows("0", "1", "10", "12", "5", "6"))
	tk.MustQuery("select * from tlist partition (p0)").Sort().Check(testkit.Rows("0", "1"))
	tk.MustQuery("select * from tlist partition (p1, p2)").Sort().Check(testkit.Rows("10", "12", "5", "6"))
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionView(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_view")
	tk.MustExec("use list_partition_view")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)

	tk.MustExec(`create table tlist (a int, b int) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14))`)
	tk.MustExec(`create definer='root'@'localhost' view vlist as select a*2 as a2, a+b as ab from tlist`)
	tk.MustExec(`create table tnormal (a int, b int)`)
	tk.MustExec(`create definer='root'@'localhost' view vnormal as select a*2 as a2, a+b as ab from tnormal`)
	for i := 0; i < 10; i++ {
		a, b := rand.Intn(15), rand.Intn(100)
		tk.MustExec(fmt.Sprintf(`insert into tlist values (%v, %v)`, a, b))
		tk.MustExec(fmt.Sprintf(`insert into tnormal values (%v, %v)`, a, b))
	}

	r1 := tk.MustQuery(`select * from vlist`).Sort()
	r2 := tk.MustQuery(`select * from vnormal`).Sort()
	r1.Check(r2.Rows())
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionAutoIncre(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_auto_incre")
	tk.MustExec("use list_partition_auto_incre")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)

	c.Assert(tk.ExecToErr(`create table tlist (a int, b int AUTO_INCREMENT) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14))`), ErrorMatches, ".*it must be defined as a key.*")

	tk.MustExec(`create table tlist (a int, b int AUTO_INCREMENT, key(b)) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14))`)

	tk.MustExec(`insert into tlist (a) values (0)`)
	tk.MustExec(`insert into tlist (a) values (5)`)
	tk.MustExec(`insert into tlist (a) values (10)`)
	tk.MustExec(`insert into tlist (a) values (1)`)
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionAutoRandom(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_auto_rand")
	tk.MustExec("use list_partition_auto_rand")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)

	c.Assert(tk.ExecToErr(`create table tlist (a int, b bigint AUTO_RANDOM) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14))`), ErrorMatches, ".*Invalid auto random.*")

	tk.MustExec(`create table tlist (a bigint auto_random, primary key(a)) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14))`)
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionInvisibleIdx(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_invisible_idx")
	tk.MustExec("use list_partition_invisible_idx")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)

	tk.MustExec(`create table tlist (a int, b int, key(a)) partition by list (a) (partition p0 values in (0, 1, 2), partition p1 values in (3, 4, 5))`)
	tk.MustExec(`alter table tlist alter index a invisible`)
	tk.HasPlan(`select a from tlist where a>=0 and a<=5`, "TableFullScan")
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionCTE(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_cte")
	tk.MustExec("use list_partition_cte")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)

	tk.MustExec(`create table tlist (a int) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14))`)

	tk.MustExec(`insert into tlist values (0), (1), (5), (6), (10)`)
	tk.MustQuery(`with tmp as (select a+1 as a from tlist) select * from tmp`).Sort().Check(testkit.Rows("1", "11", "2", "6", "7"))
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionTempTable(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_temp_table")
	tk.MustExec("use list_partition_temp_table")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec("set tidb_enable_global_temporary_table = true")
	c.Assert(tk.ExecToErr("create global temporary table t(a int, b int) partition by list(a) (partition p0 values in (0)) on commit delete rows"), ErrorMatches, ".*Cannot create temporary table with partitions.*")
}

func (s *testIntegrationPartitionSerialSuite) TestListPartitionAlterPK(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database list_partition_alter_pk")
	tk.MustExec("use list_partition_alter_pk")
	tk.MustExec("drop table if exists tlist")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec(`create table tlist (a int, b int) partition by list (a) (
    partition p0 values in (0, 1, 2, 3, 4),
    partition p1 values in (5, 6, 7, 8, 9),
    partition p2 values in (10, 11, 12, 13, 14))`)
	tk.MustExec(`alter table tlist add primary key(a)`)
	tk.MustExec(`alter table tlist drop primary key`)
	c.Assert(tk.ExecToErr(`alter table tlist add primary key(b)`), ErrorMatches, ".*must include all columns.*")
}

func (s *testIntegrationPartitionSerialSuite) TestIssue27018(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database issue_27018")
	tk.MustExec("use issue_27018")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec(`CREATE TABLE PK_LP9326 (
  COL1 tinyint(45) NOT NULL DEFAULT '30' COMMENT 'NUMERIC PK',
  PRIMARY KEY (COL1) /*T![clustered_index] CLUSTERED */
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
PARTITION BY LIST COLUMNS(col1) (
  PARTITION P0 VALUES IN (56,127,-128,-125,-40,-18,-10,-5,49,51),
  PARTITION P1 VALUES IN (-107,-97,-57,-37,4,43,99,-9,-6,45),
  PARTITION P2 VALUES IN (108,114,-85,-72,-38,-11,29,97,40,107),
  PARTITION P3 VALUES IN (-112,-95,-42,24,28,47,-103,-94,7,64),
  PARTITION P4 VALUES IN (-115,-101,-76,-47,1,19,-114,-23,-19,11),
  PARTITION P5 VALUES IN (44,95,-92,-89,-26,-21,25,-117,-116,27),
  PARTITION P6 VALUES IN (50,61,118,-110,-32,-1,111,125,-90,74),
  PARTITION P7 VALUES IN (75,121,-96,-87,-14,-13,37,-68,-58,81),
  PARTITION P8 VALUES IN (126,30,48,68)
)`)
	tk.MustExec(`insert into PK_LP9326 values(30),(48),(56)`)
	tk.MustQuery(`SELECT COL1 FROM PK_LP9326 WHERE COL1 NOT IN (621579514938,-17333745845828,2777039147338)`).Sort().Check(testkit.Rows("30", "48", "56"))
}

func (s *testIntegrationPartitionSerialSuite) TestIssue27017(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database issue_27017")
	tk.MustExec("use issue_27017")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec(`CREATE TABLE PK_LP9465 (
  COL1 mediumint(45) NOT NULL DEFAULT '77' COMMENT 'NUMERIC PK',
  PRIMARY KEY (COL1) /*T![clustered_index] CLUSTERED */
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
PARTITION BY LIST COLUMNS(col1) (
  PARTITION P0 VALUES IN (-5237720,2949267,6047247,-8317208,-6854239,-6612749,-6578207,-5649321,2450483,2953765),
  PARTITION P1 VALUES IN (5884439,-7816703,-6716210,-6050369,-5691207,6836620,5769359,-8237127,-1294367,-1228621),
  PARTITION P2 VALUES IN (-976130,-8351227,-8294140,-4800605,1370685,-7351802,-6447779,77,1367409,5965199),
  PARTITION P3 VALUES IN (7347944,7397124,8013414,-5737292,-3938813,-3687304,1307396,444598,1216072,1603451),
  PARTITION P4 VALUES IN (2518402,-8388608,-5291256,-3796824,121011,8388607,39191,2323510,3386861,4886727),
  PARTITION P5 VALUES IN (-6512367,-5922779,-3272589,-1313463,5751641,-3974640,2605656,3336269,4416436,-7975238),
  PARTITION P6 VALUES IN (-6693544,-6023586,-4201506,6416586,-3254125,-205332,1072201,2679754,1963191,2077718),
  PARTITION P7 VALUES IN (4205081,5170051,-8087893,-5805143,-1202286,1657202,8330979,5042855,7578575,-5830439),
  PARTITION P8 VALUES IN (-5244013,3837781,4246485,670906,5644986,5843443,7794811,7831812,-7704740,-2222984),
  PARTITION P9 VALUES IN (764108,3406142,8263677,248997,6129417,7556305,7939455,3526998,8239485,-5195482),
  PARTITION P10 VALUES IN (-3625794,69270,377245)
)`)
	tk.MustExec(`insert into PK_LP9465 values(8263677)`)
	tk.MustQuery(`SELECT COL1 FROM PK_LP9465 HAVING COL1>=-12354348921530`).Sort().Check(testkit.Rows("8263677"))
}

func (s *testIntegrationPartitionSerialSuite) TestIssue27012(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("create database issue_27012")
	tk.MustExec("use issue_27012")
	tk.MustExec(`set tidb_enable_list_partition = 1`)
	tk.MustExec(`CREATE TABLE IDT_LP24306 (
  COL1 tinyint(16) DEFAULT '41' COMMENT 'NUMERIC UNIQUE INDEX',
  KEY UK_COL1 (COL1) /*!80000 INVISIBLE */
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
PARTITION BY LIST COLUMNS(col1) (
  PARTITION P0 VALUES IN (-126,-36,-96,-6,-83,-123,-5,-52,-98,-124),
  PARTITION P1 VALUES IN (-2,-22,-88,-100,-60,-39,-69,-38,-11,-30),
  PARTITION P2 VALUES IN (-119,-13,-67,-91,-65,-16,0,-128,-73,-118),
  PARTITION P3 VALUES IN (-99,-56,-76,-110,-93,-114,-78,NULL)
)`)
	tk.MustExec(`insert into IDT_LP24306 values(-128)`)
	tk.MustQuery(`select * from IDT_LP24306 where col1 not between 12021 and 99 and col1 <= -128`).Sort().Check(testkit.Rows("-128"))

	tk.MustExec(`drop table if exists IDT_LP24306`)
	tk.MustExec(`CREATE TABLE IDT_LP24306 (
  COL1 tinyint(16) DEFAULT '41' COMMENT 'NUMERIC UNIQUE INDEX',
  KEY UK_COL1 (COL1) /*!80000 INVISIBLE */
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`)
	tk.MustExec(`insert into IDT_LP24306 values(-128)`)
	tk.MustQuery(`select * from IDT_LP24306 where col1 not between 12021 and 99 and col1 <= -128`).Sort().Check(testkit.Rows("-128"))
}

func genListPartition(begin, end int) string {
	buf := &bytes.Buffer{}
	buf.WriteString("(")
	for i := begin; i < end-1; i++ {
		buf.WriteString(fmt.Sprintf("%v, ", i))
	}
	buf.WriteString(fmt.Sprintf("%v)", end-1))
	return buf.String()
}
